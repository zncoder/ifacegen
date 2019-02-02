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
	receiver, output, srcPath, ifaceName, mock, mockInTest := parseFlag()

	iface := Interface{
		Interface: ifaceName,
		Receiver:  receiver,
	}

	thisPkg := newThisPackage(mockInTest)
	if mock {
		iface.PkgName = thisPkg.Name()
	}

	iface.Methods = parseMethods(thisPkg, srcPath, ifaceName)

	b := genCode(&iface)
	writeCode(output, b)
}

func newThisPackage(mockInTest bool) *types.Package {
	pkg := importPackage("", "")
	if mockInTest {
		pkg.Name += "_test"
	}
	return types.NewPackage(pkg.ImportPath, pkg.Name)
}

func newPackageQualifier(thisPkg *types.Package) types.Qualifier {
	return func(pkg *types.Package) string {
		if pkg.Path() == thisPkg.Path() || pkg.Name() == thisPkg.Name() {
			return ""
		}
		return pkg.Name()
	}
}

func parseFlag() (receiver, output, srcPath, ifaceName string, mock, mockInTest bool) {
	flag.StringVar(&receiver, "r", "", "Name of receiver, default to *{Interface}{Gen|Mock}")
	flag.StringVar(&output, "o", "", "Name of output file, default to os.Stdout")
	flag.StringVar(&ifaceName, "i", "", "Interface name, [{import_path}.]{Interface}, e.g. net/http.Handler, Foo. (Required)")
	flag.BoolVar(&mock, "m", false, "Generate mock struct if true")
	flag.BoolVar(&mockInTest, "t", false, "Put the mock struct in test package if true")
	flag.Parse()
	if ifaceName == "" {
		fmt.Fprintln(os.Stderr, "interface name is required")
		os.Exit(1)
	}

	i := strings.LastIndex(ifaceName, ".")
	if i >= 0 {
		j := strings.LastIndex(ifaceName, "/")
		if j >= i {
			log.Fatalf("malformed ifacename:%q, '.' before '/'", ifaceName)
		}
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
	fatalOnErr(err, "execute template:%s", tpl.Name())
	gen := buf.Bytes()

	if iface.PkgName == "" {
		gen, err = format.Source(gen)
	} else {
		gen, err = imports.Process("", gen, nil)
	}
	fatalOnErr(err, "format/imports of code\n`%s`", buf.Bytes())
	return gen
}

func writeCode(fn string, b []byte) {
	if fn != "" {
		err := ioutil.WriteFile(fn, b, 0600)
		fatalOnErr(err, "write code to file:%q", fn)
	} else {
		_, err := os.Stdout.Write(b)
		fatalOnErr(err, "write code to stdout")
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

func importPackage(vendorPrefix, srcPath string) *build.Package {
	// package is e.g. "net/http" if it is in GOROOT or GOPATH,
	// package is e.g. "github.com/foo/bar/vendor/golang.org/x/tools/imports" if it is a vendor package.
	if srcPath == "" {
		wd, err := os.Getwd()
		fatalOnErr(err, "getwd")
		pkg, err := build.ImportDir(wd, 0)
		fatalOnErr(err, "importdir wd:%q", wd)
		return pkg
	}

	for {
		p := srcPath
		if vendorPrefix != "" && vendorPrefix != "." {
			p = filepath.Join(vendorPrefix, "vendor", srcPath)
		}
		pkg, err := build.Import(p, "", 0)
		if err == nil {
			return pkg
		}
		if vendorPrefix == "" || vendorPrefix == "." {
			fatalOnErr(err, "import pkg:%s", srcPath)
		}
		vendorPrefix = filepath.Dir(vendorPrefix)
	}
}

func parseMethods(thisPkg *types.Package, srcPath, ifaceName string) []*Method {
	pkg := importPackage(thisPkg.Path(), srcPath)

	var srcFiles []string
	for _, fn := range pkg.GoFiles {
		srcFiles = append(srcFiles, filepath.Join(pkg.Dir, fn))
	}
	info := parseTypeInfo(srcFiles)
	iface := findInterface(info, ifaceName)

	var methods []*Method
	for i := 0; i < iface.NumMethods(); i++ {
		methods = append(methods, parseMethod(thisPkg, iface.Method(i)))
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
	fatalOnErr(err, "load")
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

func parseTuple(thisPkg *types.Package, tuple *types.Tuple, namePrefix string) (params, args string) {
	pkgQual := newPackageQualifier(thisPkg)
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

func parseMethod(thisPkg *types.Package, fn *types.Func) *Method {
	pkgQual := newPackageQualifier(thisPkg)
	sig := fn.Type().(*types.Signature)

	params, args := parseTuple(thisPkg, sig.Params(), "a")
	results, resvar := parseTuple(thisPkg, sig.Results(), "r")

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
  PanicIfNotMocked bool

  {{range $x.Methods}}
  {{.Method}}Mock {{.Sig}}{{end}}

  callCounts [{{len $.Methods}}]int32
}
{{range $x.Methods}}
func (m {{$x.Receiver}}) {{.Method}}({{.Params}}) ({{.Results}}) {
  atomic.AddInt32(&m.callCounts[call{{.Method}}], 1)
  if m.{{.Method}}Mock == nil {
    if m.PanicIfNotMocked {
      panic("{{.Method}} is not mocked")
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
