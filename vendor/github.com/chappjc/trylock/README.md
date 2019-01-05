trylock - TryLock implementation for Go
=======================================

[![Build Status](https://travis-ci.com/chappjc/trylock.svg?branch=master)](https://travis-ci.org/chappjc/trylock)
[![GoDoc](https://godoc.org/github.com/chappjc/trylock?status.svg)](https://godoc.org/github.com/chappjc/trylock)

`trylock` uses unsafe, which is sorta "unsafe", but should work until
`sync.Mutex` will change its layout (I hope it never will).

The original author of `trylock` is by LK4D4.

# Usage

```go
type LockedStruct struct {
	trylock.Mutex
}

storage := &LockedStruct{}

if storage.TryLock() {
	// do something with storage
} else {
	// return busy or use some logic for unavailable storage
}
```
