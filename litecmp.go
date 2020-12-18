// Copyright 2020 Brad Fitzpatrick. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package litecmp lets you make smaller comparable values by boxing
// a larger comparable value (such as a 16 byte string header) down
// into a globally unique 8 byte pointer.
//
// The globally unique pointers are garbage collected with weak
// references and finalizers. This package hides that.
package litecmp

import (
	"runtime"
	"sync"
	"unsafe"
)

// A Value pointer is the lite handle to an underlying comparable
// value.
//
// Because the pointer is globally unique within the process for the comparable value
// passed to the Get func, the pointer itself can be used as a comparable value.
type Value struct {
	_      [0]func() // prevent people from accidentally using value type as comparable
	cmpVal interface{}
	gen    int64
}

// Get returns the comparable value passed to the Get func
// that had returned v.
func (v *Value) Get() interface{} { return v.cmpVal }

var (
	mu     sync.Mutex
	valMap = map[interface{}]uintptr{} // to uintptr(*Value)
)

// Get returns a pointer representing the comparable value cmpVal.
//
// The returned pointer will be the same for Get(v) and Get(v2)
// if and only if v == v2.
func Get(cmpVal interface{}) *Value {
	mu.Lock()
	defer mu.Unlock()

	addr, ok := valMap[cmpVal]
	var v *Value
	if ok {
		v = (*Value)((unsafe.Pointer)(addr))
	} else {
		v = &Value{cmpVal: cmpVal}
		valMap[cmpVal] = uintptr(unsafe.Pointer(v))
	}
	curGen := v.gen + 1
	v.gen = curGen

	if curGen > 1 {
		// Need to clear it before changing it,
		// else the runtime throws.
		runtime.SetFinalizer(v, nil)
	}
	runtime.SetFinalizer(v, func(v *Value) {
		mu.Lock()
		defer mu.Unlock()
		if v.gen != curGen {
			// Lost the race. Somebody is still using us.
			return
		}
		delete(valMap, v.cmpVal)
	})
	return v

}
