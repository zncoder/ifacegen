// Ifacegen generates skeleton code of a type to satisfy an
// interface. For example, to generate code to satisfy sort.Interface,
//
//    ifacegen -r stringSlice -i sort.Interface
//
// Ifacegen can generate mock struct of an interface as well by
// specifying a package name. For example,
//
//    ifacegen -p myhttp -o httphandler_mock.go -i net/http.Handler
//
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/build"
	"go/format"
	"go/types"
	"html/template"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/tools/go/loader"
	"golang.org/x/tools/imports"
)

func main() {
	receiver, mockPkg, output, srcPath, ifaceName := parseFlag()

	parsed := Interface{
		Pkg:       mockPkg,
		Interface: ifaceName,
		Receiver:  receiver,
		Methods:   parseMethods(srcPath, ifaceName),
	}

	b := genCode(&parsed)
	writeCode(output, b)
}

func parseFlag() (receiver, mockPkg, output, srcPath, iface string) {
	flag.StringVar(&receiver, "r", "", "name of receiver, default to *{Interface}{Gen|Mock}")
	flag.StringVar(&mockPkg, "p", "", "package name of the mock struct")
	flag.StringVar(&output, "o", "", "name of output file, default to os.Stdout")
	var ifaceName string
	flag.StringVar(&ifaceName, "i", "", "interface name, {import path}.{interface}, e.g. net/http.Handler; required")
	flag.Parse()

	i := strings.LastIndex(ifaceName, ".")
	if i <= 0 || i+1 == len(ifaceName) {
		fmt.Fprintln(os.Stderr, "format of interface name is {import path}.{interface}, e.g. `net/http.Handler`")
		os.Exit(1)
	}

	return receiver, mockPkg, output, ifaceName[:i], ifaceName[i+1:]
}

func genCode(parsed *Interface) []byte {
	var tpl *template.Template
	var sfx string
	if parsed.Pkg != "" {
		tpl = mockTpl
		sfx = "Mock"
	} else {
		tpl = genTpl
		sfx = "Gen"
	}

	if parsed.Receiver == "" {
		parsed.Receiver = "*" + parsed.Interface + sfx
	}
	parsed.Struct = strings.TrimPrefix(parsed.Receiver, "*")

	var buf bytes.Buffer
	err := tpl.Execute(&buf, parsed)
	if err != nil {
		log.Fatalf("execute template:%s err:%v", tpl.Name(), err)
	}
	gen := buf.Bytes()

	if parsed.Pkg == "" {
		gen, err = format.Source(gen)
	} else {
		gen, err = imports.Process("", gen, nil)
	}
	if err != nil {
		log.Fatalf("format/imports err:%v of code\n`%s`", err, buf.Bytes())
	}
	return gen
}

func writeCode(fn string, b []byte) {
	if fn != "" {
		if err := ioutil.WriteFile(fn, b, 0600); err != nil {
			log.Fatalf("write file:%q err:%v", fn, err)
		}
	} else {
		if _, err := os.Stdout.Write(b); err != nil {
			log.Fatalf("write err:%v", err)
		}
	}
}

type Method struct {
	Method     string
	Sig        string
	Params     string
	Args       string
	Results    string
	ResultVars string
}

type Interface struct {
	Pkg       string
	Interface string
	Struct    string
	Receiver  string
	Methods   []*Method
}

func parseMethods(srcPath, ifaceName string) []*Method {
	pkg, err := build.Import(srcPath, "", 0)
	if err != nil {
		log.Fatalf("import pkg:%q err:%v", srcPath, err)
	}

	var srcFiles []string
	for _, fn := range pkg.GoFiles {
		srcFiles = append(srcFiles, filepath.Join(pkg.Dir, fn))
	}
	info := parseTypeInfo(srcFiles)
	iface := findInterface(info, ifaceName)

	var methods []*Method
	for i := 0; i < iface.NumMethods(); i++ {
		methods = append(methods, parseMethod(iface.Method(i)))
	}
	return methods
}

func parseTypeInfo(srcFiles []string) *types.Info {
	var conf loader.Config
	conf.CreateFromFilenames("", srcFiles...)
	conf.AllowErrors = true
	conf.TypeChecker.Error = func(error) {}
	conf.TypeChecker.DisableUnusedImportCheck = true
	conf.TypeCheckFuncBodies = func(path string) bool { return false }

	prog, err := conf.Load()
	if err != nil {
		log.Fatalf("load err:%v", err)
	}
	return &prog.Created[0].Info
}

func findInterface(info *types.Info, name string) *types.Interface {
	for k, o := range info.Defs {
		if k.Name == name {
			iface, ok := o.Type().Underlying().(*types.Interface)
			if ok {
				return iface
			}
		}
	}
	log.Fatalf("interface:%s is not found", name)
	return nil
}

func parseTuple(tuple *types.Tuple, namePrefix string) (params, args string) {
	var errNameUsed bool
	for i := 0; i < tuple.Len(); i++ {
		v := tuple.At(i)
		name := v.Name()
		ty := v.Type().String()

		if name == "" && namePrefix != "" {
			if ty == "error" && !errNameUsed {
				errNameUsed = true
				name = "err"
			} else {
				name = namePrefix + strconv.Itoa(i)
			}
		}
		if i != 0 {
			params += ", "
			args += ", "
		}
		params += name + " " + v.Type().String()
		args += name
	}
	return params, args
}

func parseMethod(fn *types.Func) *Method {
	sig := fn.Type().(*types.Signature)

	params, args := parseTuple(sig.Params(), "a")
	results, resvar := parseTuple(sig.Results(), "r")

	return &Method{
		Method:     fn.Name(),
		Sig:        sig.String(),
		Params:     params,
		Args:       args,
		Results:    results,
		ResultVars: resvar,
	}
}

var genTpl = template.Must(template.New("gen").Parse(`{{with $x := .}}{{range .Methods}}
func (m {{$x.Receiver}}) {{.Method}}({{.Params}}) ({{.Results}}) {
}
{{end}}
{{end}}
`))

var mockTpl = template.Must(template.New("mock").Parse(`// @generated by ifacegen
{{with $x := .}}
package {{$x.Pkg}}

const (
  {{range $i, $m := $x.Methods}}call{{$m.Method}} = {{$i}}
  {{end}}
)

type {{$x.Struct}} struct {
  T *testing.T
  FailIfNotSet bool

  {{range $x.Methods}}
  {{.Method}}Mock {{.Sig}}{{end}}

  callCounts [{{len $.Methods}}]int32
}
{{range $x.Methods}}
func (m {{$x.Receiver}}) {{.Method}}({{.Params}}) ({{.Results}}) {
  atomic.AddInt32(&m.callCounts[call{{.Method}}], 1)
  if m.{{.Method}}Mock == nil {
    if m.FailIfNotSet {
      m.T.Error("{{.Method}} is not mocked")
    }
    return {{.ResultVars}}
  }
  {{if .Results}}return {{end}}m.{{.Method}}Mock({{.Args}})
}

func (m {{$x.Receiver}}) {{.Method}}CallCount() int {
  return int(atomic.LoadInt32(&m.callCounts[call{{.Method}}]))
}
{{end}}
{{end}}
`))
