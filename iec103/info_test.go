// SPDX-License-Identifier: MIT

package iec103

import (
	"sync"
	"testing"
)

func TestStandardInfo(t *testing.T) {
	// Table 8: INF 0 has two entries in the monitor direction.
	if got := StandardInfo(Monitor, 0); len(got) != 2 {
		t.Fatalf("%v", got)
	}
	if got := StandardInfo(Control, 245); len(got) != 1 || got[0].Causes[0] != CauseGI {
		t.Fatalf("%v", got)
	}
	if got := StandardInfo(Monitor, 34); got != nil {
		t.Fatalf("%v", got)
	}
	// Every table entry lists at least one type and cause.
	for _, tab := range [][]InfoSpec{monitorInfo, controlInfo} {
		for _, s := range tab {
			if len(s.Types) == 0 || len(s.Causes) == 0 || s.Name == "" {
				t.Errorf("incomplete %+v", s)
			}
		}
	}
}

func TestCatalog(t *testing.T) {
	var c Catalog
	cases := []struct {
		dir  Direction
		typ  TypeID
		fun  FunctionType
		inf  InfoNumber
		want string
	}{
		{Monitor, TypeGITermination, FunGlobal, 0, "end of general interrogation"},
		{Monitor, TypeTimeSync, FunGlobal, 0, "time synchronization"},
		{Monitor, TypeTimeTagged, FunDistance, 16, "auto-recloser active"},
		{Monitor, TypeIdentification, FunOvercurrent, InfResetCU, "reset CU"},
		{Control, TypeGeneralCommand, FunLineDiff, InfLEDReset, "LED reset"},
		{Control, TypeGenericCommand, FunGeneric, InfReadEntry, "read value or attribute of a single entry"},
		{Monitor, TypeMeasurands2, FunDistance, InfMeasurandsIVPQf, "measurands IL1,2,3, VL1,2,3, P, Q, f"},
	}
	for _, x := range cases {
		s, ok := c.Lookup(x.dir, x.typ, x.fun, x.inf)
		if !ok || s.Name != x.want {
			t.Errorf("%v %v %v %v: %q %v", x.dir, x.typ, x.fun, x.inf, s.Name, ok)
		}
	}
	// GLB is mandatory for INF 0, GEN for the generic functions.
	if _, ok := c.Lookup(Monitor, TypeGITermination, FunDistance, 0); ok {
		t.Fatal("end of GI with FUN 128")
	}
	if _, ok := c.Lookup(Control, TypeGenericCommand, FunDistance, InfReadEntry); ok {
		t.Fatal("generic read with FUN 128")
	}
	// Wrong type, private FUN.
	if _, ok := c.Lookup(Monitor, TypeMeasurands1, FunDistance, 16); ok {
		t.Fatal("INF 16 in ASDU 3")
	}
	if _, ok := c.Lookup(Monitor, TypeTimeTagged, 17, 16); ok {
		t.Fatal("private FUN in the standard tables")
	}
	// Private entries.
	c.Add(Monitor, 17, InfoSpec{Inf: 16, Name: "vendor signal", Types: []TypeID{TypeTimeTagged}})
	c.Add(Monitor, 17, InfoSpec{Inf: 99, Name: "any type"})
	c.Add(Monitor, FunDistance, InfoSpec{Inf: 16, Name: "override", Types: []TypeID{TypeTimeTagged}})
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if s, ok := c.Lookup(Monitor, TypeTimeTagged, 17, 16); !ok || s.Name != "vendor signal" {
				t.Error(s, ok)
			}
		})
	}
	wg.Wait()
	if s, ok := c.Lookup(Monitor, TypeRelTimeTagged, 17, 99); !ok || s.Name != "any type" {
		t.Fatal(s, ok)
	}
	if s, _ := c.Lookup(Monitor, TypeTimeTagged, FunDistance, 16); s.Name != "override" {
		t.Fatal(s)
	}
	if _, ok := c.Lookup(Monitor, TypeRelTimeTagged, 17, 16); ok {
		t.Fatal("type-specific entry matched another type")
	}
}
