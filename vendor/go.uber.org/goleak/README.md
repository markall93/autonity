# goleak [![GoDoc][doc-img]][doc] [![Build Status][ci-img]][ci] [![Coverage Status][cov-img]][cov]

Goroutine leak detector to help avoid Goroutine leaks.

## Development Status: Alpha

goleak is still in development, and APIs are still in flux.

## Installation

You can use `go get` to get the latest version:

`go get -u go.uber.org/goleak`

`goleak` also supports semver releases. It is compatible with Go 1.5+.

## Quick Start

To verify that there are no unexpected goroutines running at the end of a test:

```go
func TestA(t *testing.T) {
	defer goleak.VerifyNone(t)

	// test logic here.
}
```

Instead of checking for leaks at the end of every test, `goleak` can also be run
at the end of every test package by creating a `TestMain` function for your
package:

```go
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
```

## Determine Source of Package Leaks

When verifying leaks using `TestMain`, the leak test is only run once after all tests
have been run. This is typically enough to ensure there's no goroutines leaked from
tests, but when there are leaks, it's hard to determine which test is causing them.

You can use the following bash script to determine the source of the failing test:

```sh
# Create a test binary which will be used to run each test individually
$ go test -c -o tests

# Run each test individually, printing "." for successful tests, or the test name
# for failing tests.
$ for test in $(go test -list . | grep "^Test"); do ./tests -test.run "^$test\$" &>/dev/null && echo -n "." || echo "\n$test failed"; done
```

This will only print names of failing tests which can be investigated individually. E.g.,

```
.....
TestLeakyTest failed
.......
```


[doc-img]: https://godoc.org/go.uber.org/goleak?status.svg
[doc]: https://godoc.org/go.uber.org/goleak
[ci-img]: https://travis-ci.com/uber-go/goleak.svg?branch=master
[ci]: https://travis-ci.com/uber-go/goleak
[cov-img]: https://codecov.io/gh/uber-go/goleak/branch/master/graph/badge.svg
[cov]: https://codecov.io/gh/uber-go/goleak
[benchmarking suite]: https://github.com/uber-go/goleak/tree/master/benchmarks
[glide.lock]: https://github.com/uber-go/goleak/blob/master/glide.lock
