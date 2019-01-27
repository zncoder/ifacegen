# ifacegen
Ifacegen generates skeleton code of a type to satisfy an interface.

## Code Snippet
You can use `ifacegen` to generate method stubs of an interface.

For example, to implement sort.Interface of a type `fooSlice`, you run `ifacegen` to generate the method stubs,
```
ifacegen -r fooSlice -i sort.Interface
```

This generates the following code snippet and print to stdout,
```
func (m fooSlice) Len() int {
}

func (m fooSlice) Less(i int, j int) bool {
}

func (m fooSlice) Swap(i int, j int) {
}
```
Now you can fill in the implementation details of these methods.

## Mock
Ifacegen can generate mock struct to satisfy an interface as well.

For example, to mock `net.Conn`, you run `ifacegen` to generate a mock struct,
```
ifacegen -p mynet -i net.Conn -o mynet_mock.go
```

This generates a file `mynet_mock.go` with the code,
```
// @generated by ifacegen

package myio

type ReadWriterMock struct {
	ReadMock  func(p []byte) (n int, err error)
	WriteMock func(p []byte) (n int, err error)
}

func (m *ReadWriterMock) Read(p []byte) (n int, err error) {
	if m.ReadMock == nil {
		panic("Read is not mocked")
	}
	return m.ReadMock(p)
}

func (m *ReadWriterMock) Write(p []byte) (n int, err error) {
	if m.WriteMock == nil {
		panic("Write is not mocked")
	}
	return m.WriteMock(p)
}
```

Now you can use `ReadWriterMock` and set `ReadMock` and/or `WriteMock` in your tests,
```
func TestFoo(t *testing.T) {
  rw := ReadWriterMock{
    ReadMock: func(b []byte) (int, error) {
      return copy(b, "hello"), nil
    },
  }
}
```