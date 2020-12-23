// Copyright 2020 Brad Fitzpatrick. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package intern

import (
	"runtime"
	"testing"
)

func TestBasics(t *testing.T) {
	clearMap()
	foo := Get("foo")
	bar := Get("bar")
	foo2 := Get("foo")
	bar2 := Get("bar")

	if foo.Get() != foo2.Get() {
		t.Error("foo values differ")
	}
	if foo.Get() != "foo" {
		t.Error("foo.Get not foo")
	}
	if foo != foo2 {
		t.Error("foo pointers differ")
	}

	if bar.Get() != bar2.Get() {
		t.Error("bar values differ")
	}
	if bar.Get() != "bar" {
		t.Error("bar.Get not bar")
	}
	if bar != bar2 {
		t.Error("bar pointers differ")
	}

	if n := mapLen(); n != 2 {
		t.Errorf("map len = %d; want 2", n)
	}

	wantEmpty(t)
}

func wantEmpty(t testing.TB) {
	t.Helper()
	const gcTries = 5000
	for try := 0; try < gcTries; try++ {
		runtime.GC()
		n := mapLen()
		if n == 0 {
			break
		}
		if try == gcTries-1 {
			t.Errorf("map len = %d after (%d GC tries); want 0", n, gcTries)
		}
	}
}

func mapLen() int {
	mu.Lock()
	defer mu.Unlock()
	return len(valMap)
}

func clearMap() {
	mu.Lock()
	defer mu.Unlock()
	for k := range valMap {
		delete(valMap, k)
	}
}
