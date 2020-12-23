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
	"os"
	"runtime"
	"strconv"
	"sync"
	"unsafe"

	_ "go4.org/unsafe/assume-no-moving-gc"
)

// A Value pointer is the handle to an underlying comparable value.
// See func Get for how Value pointers may be used.
type Value struct {
	_      [0]func() // prevent people from accidentally using value type as comparable
	cmpVal interface{}
	// resurrected is guarded by mu (for all instances of Value).
	resurrected bool
}

// Get returns the comparable value passed to the Get func
// that returned v.
func (v *Value) Get() interface{} { return v.cmpVal }

var (
	// mu guards valMap, a weakref map of *Value by underlying value.
	// It also guards the refs field of all *Values.
	mu      sync.Mutex
	valMap  = map[interface{}]uintptr{} // to uintptr(*Value)
	valSafe = safeMap()                 // non-nil in safe+leaky mode
)

// safeMap returns a non-nil map if we're in safe-but-leaky mode,
// as controlled by GO4_INTERN_SAFE_BUT_LEAKY.
func safeMap() map[interface{}]*Value {
	if v, _ := strconv.ParseBool(os.Getenv("GO4_INTERN_SAFE_BUT_LEAKY")); v {
		return map[interface{}]*Value{}
	}
	return nil
}

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

	var v *Value
	if valSafe != nil {
		v = valSafe[cmpVal]
	} else if addr, ok := valMap[cmpVal]; ok {
		v = (*Value)((unsafe.Pointer)(addr))
		// We've materialized a *Value out of thin air, in violation
		// of Go's unsafety rules. The GC won't "see" this pointer
		// until the next GC cycle. This is also true of normal
		// pointers. Unlike normal pointers however, it's possible
		// that the GC has just finished a mark phase in which it
		// concluded that v was unreferenced, and thus collectable.
		//
		// By setting resurrected here, we signal to v's finalizer
		// that the Value needs to remain alive for at least one more
		// GC cycle, long enough for the GC to become aware of v's
		// existence. See the comments in finalize for the other leg
		// of this interaction.
		v.resurrected = true
		return v
	}
	// When we first create a value, v is a "correct" pointer from the
	// runtime's perspective. There is no risk of concurrent
	// collection, so we can set resurrected=false.
	v = &Value{cmpVal: cmpVal, resurrected: false}
	valMap[cmpVal] = uintptr(unsafe.Pointer(v))
	runtime.SetFinalizer(v, finalize)
	return v
}

func finalize(v *Value) {
	mu.Lock()
	defer mu.Unlock()

	// Now that the finalizer is holding mu, we're sure that Get
	// cannot unsafely materialize more references to v. But maybe it
	// already did materialize an unsafe reference previously?
	if v.resurrected {
		// We may have lost a race with Get, and there may be a new
		// pointer to v that the GC is unaware of. We can't let it
		// delete v. To prevent that, we reinstall ourselves as
		// finalizer, which will make the value persist until the next
		// GC cycle. We also clear the sentinel, so that if there
		// truly are no more references to v, the next finalizer run
		// will clean up.
		//
		// Note that this check is not precise. The call to Get that
		// set resurrected may have happened thousands of GC cycles
		// ago, and the GC may be entirely correct that v is
		// collectable. But we can't know for certain until we got a
		// chance to run and clear resurrected, so even in the
		// non-racy case, Values that have been used more than once
		// take 2 GC cycles to actually be freed.
		v.resurrected = false
		runtime.SetFinalizer(v, finalize)
	} else {
		// There have been no new pointers to v materialized since the
		// previous GC cycle, and because we're holding mu Get can't
		// create any right now. It's safe to drop v.
		delete(valMap, v.cmpVal)
	}
}

// Interning is simple if you don't require that unused values be
// garbage collectable. But we do require that; we don't want to be
// DOS vector. We do this by using a uintptr to hide the pointer from
// the garbage collector, and using a finalizer to eliminate the
// pointer when no other code is using it.
//
// The obvious implementation of this is to use a
// map[interface{}]uintptr-of-*interface{}, and set up a finalizer to
// delete from the map. Unfortunately, that contains a logical race.
// The finalizer can start concurrently with a new request to look up
// a pointer with no other references to it. The new pointer lookup
// creates a new reference to an existing (almost-GC-able) pointer.
// The finalizer then continues to run, deleting the pointer from the
// map. Future pointer lookups will create a new pointer, breaking
// the comparability invariant.
//
// The finalizer fundamentally needs to know that no other references
// have been created since this finalizer was set up. There is no
// external synchronization that can provide that. Instead, Values
// carry a sentinel indicating that new references to it were
// created. The finalizer can use this to forcibly delay collection
// (by re-setting a finalizer within the finalizer) as long as there
// is any doubt over whether new unsafe references exist.
//
// @josharian has a mild lingering concern about this approach. It is
// possible to for the runtime to concurrently decide it needs to
// _execute_ a finalizer and also _remove_ the need for that finalizer
// to run, because a new reference has appeared. It is possible that
// this could cause a data race in the runtime. This is not a normal
// thing to have happen; it requires unsafe hiding of a pointer in a
// uintptr. It thus might not be tested for or protected against in
// the runtime. Hopefully this will not prove to be a problem in
// practice.
//
// @ianlancetaylor commented in
// https://github.com/golang/go/issues/41303#issuecomment-717401656
// that it is possible to implement weak references in terms of
// finalizers without unsafe. Unfortunately, the approach he outlined
// does not work here, for two reasons. First, there is no way to
// construct a strong pointer out of a weak pointer; our map stores
// weak pointers, but we must return strong pointers to callers.
// Second, and more fundamentally, we must return not just _a_ strong
// pointer to callers, but _the same_ strong pointer to callers. In
// order to return _the same_ strong pointer to callers, we must track
// it, which is exactly what we cannot do with strong pointers.
//
// See https://github.com/inetaf/netaddr/issues/53 for more
// discussion, and https://github.com/go4org/intern/issues/2 for an
// illustration of the subtleties at play.
