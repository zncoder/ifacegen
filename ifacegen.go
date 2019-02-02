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

// TODO:
// - handle interface that is in vendor

func main() {
	receiver, output, srcPath, ifaceName, mock, mockInTest := parseFlag()

	iface := Interface{
		Interface: ifaceName,
		Receiver:  receiver,
	}

	pkgQual, pkgName := qualifyPackage(mockInTest)
	if mock {
		iface.PkgName = pkgName
	}

	iface.Methods = parseMethods(pkgQual, srcPath, ifaceName)

	b := genCode(&iface)
	writeCode(output, b)
}

func qualifyPackage(mockInTest bool) (types.Qualifier, string) {
	pkg := importPackage("")
	log.Printf("this pkg: name:%s,dir:%s,path:%s", pkg.Name, pkg.Dir, pkg.ImportPath)
	name := pkg.Name
	if mockInTest {
		name += "_test"
	}

	tpkg := types.NewPackage(pkg.ImportPath, name)
	q := func(pkg *types.Package) string {
		log.Printf("thispkg.path:%s pkg.path:%s", tpkg.Path(), pkg.Path())
		if pkg.Path() == tpkg.Path() || pkg.Name() == name {
			return ""
		}
		return pkg.Name()
	}
	return q, name
}

func parseFlag() (receiver, output, srcPath, ifaceName string, mock, mockInTest bool) {
	flag.StringVar(&receiver, "r", "", "name of receiver, default to *{Interface}{Gen|Mock}")
	flag.StringVar(&output, "o", "", "name of output file, default to os.Stdout")
	flag.StringVar(&ifaceName, "i", "", "interface name, [{import path}.]{interface}, e.g. net/http.Handler, Foo; required")
	flag.BoolVar(&mock, "m", false, "true to generate mock struct")
	flag.BoolVar(&mockInTest, "t", false, "true to put the mock struct in test package")
	flag.Parse()
	if ifaceName == "" {
		fmt.Fprintln(os.Stderr, "interface name is required")
		os.Exit(1)
	}

	i := strings.LastIndex(ifaceName, ".")
	if i >= 0 {
		srcPath = ifaceName[:i]
		ifaceName = ifaceName[i+1:]
	}
	return receiver, output, srcPath, ifaceName, mock, mockInTest
}

func genCode(iface *Interface) []byte {
	var tpl *template.Template
	var sfx string
	if iface.PkgName != "" {
		tpl = mockTpl
		sfx = "Mock"
	} else {
		tpl = genTpl
		sfx = "Gen"
	}

	if iface.Receiver == "" {
		iface.Receiver = "*" + iface.Interface + sfx
	}
	iface.Struct = strings.TrimPrefix(iface.Receiver, "*")

	var buf bytes.Buffer
	err := tpl.Execute(&buf, iface)
	if err != nil {
		log.Fatalf("execute template:%s err:%v", tpl.Name(), err)
	}
	gen := buf.Bytes()

	if iface.PkgName == "" {
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
	PkgName   string
	Interface string
	Struct    string
	Receiver  string
	Methods   []*Method
}

func importPackage(srcPath string) *build.Package {
	// package is e.g. "net/http" if it is in GOROOT or GOPATH,
	// package is e.g. "github.com/foo/bar/vendor/golang.org/x/tools/imports" if it is a vendor package.
	var pkg *build.Package
	var err error
	if srcPath != "" {
		pkg, err = build.Import(srcPath, "", 0)
		fatalOnErr(err, "import pkg:%s", srcPath)
	} else {
		wd, ee := os.Getwd()
		fatalOnErr(ee, "getwd")
		pkg, err = build.ImportDir(wd, 0)
		fatalOnErr(err, "importdir wd:%q", wd)
	}
	return pkg
}

func parseMethods(pkgQual types.Qualifier, srcPath, ifaceName string) []*Method {
	pkg := importPackage(srcPath)

	var srcFiles []string
	for _, fn := range pkg.GoFiles {
		srcFiles = append(srcFiles, filepath.Join(pkg.Dir, fn))
	}
	info := parseTypeInfo(srcFiles)
	iface := findInterface(info, ifaceName)

	var methods []*Method
	for i := 0; i < iface.NumMethods(); i++ {
		methods = append(methods, parseMethod(pkgQual, iface.Method(i)))
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

func parseTuple(pkgQual types.Qualifier, tuple *types.Tuple, namePrefix string) (params, args string) {
	var errNameUsed bool
	for i := 0; i < tuple.Len(); i++ {
		v := tuple.At(i)
		name := v.Name()
		ty := types.TypeString(v.Type(), pkgQual)

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
		params += name + " " + ty
		args += name
	}
	return params, args
}

func parseMethod(pkgQual types.Qualifier, fn *types.Func) *Method {
	sig := fn.Type().(*types.Signature)

	params, args := parseTuple(pkgQual, sig.Params(), "a")
	results, resvar := parseTuple(pkgQual, sig.Results(), "r")

	return &Method{
		Method:     fn.Name(),
		Sig:        types.TypeString(sig, pkgQual),
		Params:     params,
		Args:       args,
		Results:    results,
		ResultVars: resvar,
	}
}

func fatalOnErr(err error, format string, args ...interface{}) {
	if err != nil {
		log.Fatalf(format+" err:"+err.Error(), args...)
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
package {{$x.PkgName}}

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
