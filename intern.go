// Copyright 2020 Brad Fitzpatrick. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package intern lets you make smaller comparable values by boxing
// a larger comparable value (such as a 16 byte string header) down
// into a globally unique 8 byte pointer.
//
// The globally unique pointers are garbage collected with weak
// references and finalizers. This package hides that.
//
// The GitHub repo is https://github.com/go4org/intern
package intern // import "go4.org/intern"

import (
	"runtime"
	"sync"
	"unsafe"

	_ "go4.org/unsafe/assume-no-moving-gc"
)

// A Value pointer is the handle to an underlying comparable value.
// See func Get for how Value pointers may be used.
type Value struct {
	_      [0]func() // prevent people from accidentally using value type as comparable
	cmpVal interface{}
	// refs is guarded by mu (for all instances of Value).
	// It is incremented whenever this Value is returned from Get.
	refs int64
}

// Get returns the comparable value passed to the Get func
// that returned v.
func (v *Value) Get() interface{} { return v.cmpVal }

var (
	// mu guards valMap, a weakref map of *Value by underlying value.
	// It also guards the refs field of all *Values.
	mu     sync.Mutex
	valMap = map[interface{}]uintptr{} // to uintptr(*Value)
)

// We play unsafe games that violate Go's rules (and assume a non-moving
// collector). So we quiet Go here.
// See the comment below Get for more implementation details.
//go:nocheckptr

// Get returns a pointer representing the comparable value cmpVal.
//
// The returned pointer will be the same for Get(v) and Get(v2)
// if and only if v == v2, and can be used as a map key.
func Get(cmpVal interface{}) *Value {
	mu.Lock()
	defer mu.Unlock()

	addr, ok := valMap[cmpVal]
	var v *Value
	if ok {
		v = (*Value)((unsafe.Pointer)(addr))
		v.refs++
		return v
	}
	v = &Value{cmpVal: cmpVal, refs: 1}
	valMap[cmpVal] = uintptr(unsafe.Pointer(v))
	runtime.SetFinalizer(v, finalize)
	return v
}

func finalize(v *Value) {
	mu.Lock()
	defer mu.Unlock()
	v.refs--
	if v.refs == 0 {
		delete(valMap, v.cmpVal)
		return
	}
	runtime.SetFinalizer(v, finalize)
}

// Interning is simple if you don't require that unused values be garbage collectable.
// But we do require that; we don't want to be DOS vector.
// We do this by using a uintptr to hide the pointer from the garbage collector,
// and using a finalizer to eliminate the pointer when no other code is using it.
//
// The obvious implementation of this is to use a map[interface{}]uintptr-of-*interface{},
// and set up a finalizer to delete from the map.
// Unfortunately, that contains a logical race.
// The finalizer can start concurrently with a new request
// to look up a pointer with no other references to it.
// The new pointer lookup creates a new reference to an existing (almost-GC-able) pointer.
// The finalizer then continues to run, deleting the pointer from the map.
// Future pointer lookups will create a new pointer, breaking the comparability invariant.
//
// The finalizer fundamentally needs to know that no other
// references have been created since this finalizer was set up.
// There is no external synchronization that can provide that.
// Instead, every time we create a new Value pointer, we set a new finalizer.
// That finalizer knows the latest Value pointer at the time that it was created;
// that is the gen (generation) field in type Value.
// When the finalizer runs, if its generation differs from the current Value generation,
// another reference must have been created in the interim,
// so it should not delete the *Value from the map.
// Another, later, finalizer will take care of that.
// The Value generation field is protected by mu, providing a consistent view.
//
// @josharian has a mild lingering concern about this approach.
// It is possible to for the runtime to concurrently decide it needs to _execute_ a finalizer and
// also _remove_ the need for that finalizer to run, because a new reference has appeared.
// It is possible that this could cause a data race in the runtime.
// This is not a normal thing to have happen; it requires unsafe hiding of a pointer in a uintptr.
// It thus might not be tested for or protected against in the runtime.
// Hopefully this will not prove to be a problem in practice.
//
// @ianlancetaylor commented in https://github.com/golang/go/issues/41303#issuecomment-717401656
// that it is possible to implement weak references in terms of finalizers without unsafe.
// Unfortunately, the approach he outlined does not work here, for two reasons.
// First, there is no way to construct a strong pointer out of a weak pointer;
// our map stores weak pointers, but we must return strong pointers to callers.
// Second, and more fundamentally, we must return not just _a_ strong pointer to callers,
// but _the same_ strong pointer to callers.
// In order to return _the same_ strong pointer to callers, we must track it,
// which is exactly what we cannot do with strong pointers.
//
// See https://github.com/inetaf/netaddr/issues/53 for more discussion.
