package testdata

type VariadicMethod interface {
	Foo(...string) bool
}

type Underscore interface {
	Foo(string) (a int, _ error)
}
