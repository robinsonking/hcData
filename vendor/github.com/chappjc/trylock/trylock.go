package trylock

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

const mutexLocked = 1

// Mutex is sync.Mutex with the ability to try to Lock.
type Mutex struct {
	sync.Mutex
}

// TryLock tries to lock the Mutex. It returns true in case of success, false
// otherwise.
func (m *Mutex) TryLock() bool {
	return atomic.CompareAndSwapInt32((*int32)(unsafe.Pointer(&m.Mutex)), 0, mutexLocked)
}

// TODO: RWMutex
