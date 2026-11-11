// SPDX-License-Identifier: MIT

package iec103

import (
	"slices"
	"sync"
)

// Direction is the direction of transmission an information number is
// defined for.
type Direction uint8

// Directions.
const (
	Monitor Direction = iota // monitor direction: protection equipment to control system
	Control                  // control direction: control system to protection equipment
)

// InfoSpec is the semantics of one information number (clause 7.2.5.2,
// tables 8 to 18).
type InfoSpec struct {
	Inf  InfoNumber
	Name string
	// GI reports information included in the general interrogation.
	GI bool
	// Types and Causes are the type identifications and causes of
	// transmission the table lists for it.
	Types  []TypeID
	Causes []Cause
	// Funs are the function types the table lists. For the system and
	// generic functions (GLB, GEN) they are mandatory; elsewhere they are
	// typical. Empty means "according to the main function" (table 8).
	Funs []FunctionType
}

func ty(t ...TypeID) []TypeID             { return t }
func co(c ...Cause) []Cause               { return c }
func fn(f ...FunctionType) []FunctionType { return f }

var (
	glb = fn(FunGlobal)
	gen = fn(FunGeneric)

	fDOL   = fn(FunDistance, FunOvercurrent, FunLineDiff)
	fDO    = fn(FunDistance, FunOvercurrent)
	fD     = fn(FunDistance)
	fDOTL  = fn(FunDistance, FunOvercurrent, FunTransformerDiff, FunLineDiff)
	cStat  = co(1, 7, 9, 11, 12, 20, 21)
	cStatN = co(1, 7, 11, 12, 20, 21)
	cGI11  = co(9, 11)
	cAux   = co(1, 7, 9, 11)
	c179   = co(1, 7, 9)
	c17    = co(1, 7)
	c27    = co(2, 7)
)

// monitorInfo is tables 8 to 15 (monitor direction).
var monitorInfo = []InfoSpec{
	// Table 8: system functions.
	{0, "end of general interrogation", false, ty(8), co(10), glb},
	{0, "time synchronization", false, ty(6), co(8), glb},
	{2, "reset FCB", false, ty(5), co(3), nil},
	{3, "reset CU", false, ty(5), co(4), nil},
	{4, "start/restart", false, ty(5), co(5), nil},
	{5, "power on", false, ty(5), co(6), nil},
	// Table 9: status indications.
	{16, "auto-recloser active", true, ty(1), cStat, fDOL},
	{17, "teleprotection active", true, ty(1), cStat, fDO},
	{18, "protection active", true, ty(1), cStat, fDOTL},
	{19, "LED reset", false, ty(1), cStatN, fDOTL},
	{20, "monitor direction blocked", true, ty(1), cGI11, fDOTL},
	{21, "test mode", true, ty(1), cGI11, fDOTL},
	{22, "local parameter setting", true, ty(1), cGI11, fDOTL},
	{23, "characteristic 1", true, ty(1), cStat, fD},
	{24, "characteristic 2", true, ty(1), cStat, fD},
	{25, "characteristic 3", true, ty(1), cStat, fD},
	{26, "characteristic 4", true, ty(1), cStat, fD},
	{27, "auxiliary input 1", true, ty(1), cAux, fDOTL},
	{28, "auxiliary input 2", true, ty(1), cAux, fDOTL},
	{29, "auxiliary input 3", true, ty(1), cAux, fDOTL},
	{30, "auxiliary input 4", true, ty(1), cAux, fDOTL},
	// Table 10: supervision indications.
	{32, "measurand supervision I", true, ty(1), c179, fDO},
	{33, "measurand supervision V", true, ty(1), c179, fDO},
	{35, "phase sequence supervision", true, ty(1), c179, fDO},
	{36, "trip circuit supervision", true, ty(1), c179, fDOTL},
	{37, "I>> back-up operation", true, ty(1), c179, fD},
	{38, "VT fuse failure", true, ty(1), c179, fDO},
	{39, "teleprotection disturbed", true, ty(1), c179, fDOL},
	{46, "group warning", true, ty(1), c179, fDOTL},
	{47, "group alarm", true, ty(1), c179, fDOTL},
	// Table 11: earth fault indications.
	{48, "earth fault L1", true, ty(1), c179, fDO},
	{49, "earth fault L2", true, ty(1), c179, fDO},
	{50, "earth fault L3", true, ty(1), c179, fDO},
	{51, "earth fault forward, i.e. line", true, ty(1), c179, fDO},
	{52, "earth fault reverse, i.e. busbar", true, ty(1), c179, fDO},
	// Table 12: fault indications.
	{64, "start/pick-up L1", true, ty(2), c179, fDOL},
	{65, "start/pick-up L2", true, ty(2), c179, fDOL},
	{66, "start/pick-up L3", true, ty(2), c179, fDOL},
	{67, "start/pick-up N", true, ty(2), c179, fDOL},
	{68, "general trip", false, ty(2), c17, fDOTL},
	{69, "trip L1", false, ty(2), c17, fDOTL},
	{70, "trip L2", false, ty(2), c17, fDOTL},
	{71, "trip L3", false, ty(2), c17, fDOTL},
	{72, "trip I>> (back-up operation)", false, ty(2), c17, fD},
	// The text we hold lists TYP 2 for INF 73, although ASDU 4 is the one
	// that carries the fault location SCL (clauses 7.2.6.20 and 7.3.1.4):
	// both are listed so that an ASDU 4 with INF 73 resolves.
	{73, "fault location X in ohms", false, ty(2, 4), c17, fDO},
	{74, "fault forward/line", false, ty(2), c17, fDO},
	{75, "fault reverse/busbar", false, ty(2), c17, fDO},
	{76, "teleprotection signal transmitted", false, ty(2), c17, fDO},
	{77, "teleprotection signal received", false, ty(2), c17, fDO},
	{78, "zone 1", false, ty(2), c17, fD},
	{79, "zone 2", false, ty(2), c17, fD},
	{80, "zone 3", false, ty(2), c17, fD},
	{81, "zone 4", false, ty(2), c17, fD},
	{82, "zone 5", false, ty(2), c17, fD},
	{83, "zone 6", false, ty(2), c17, fD},
	{84, "general start/pick-up", true, ty(2), c179, fDOTL},
	{85, "breaker failure", false, ty(2), c17, fDO},
	{86, "trip measuring system L1", false, ty(2), c17, fn(FunTransformerDiff)},
	{87, "trip measuring system L2", false, ty(2), c17, fn(FunTransformerDiff)},
	{88, "trip measuring system L3", false, ty(2), c17, fn(FunTransformerDiff)},
	{89, "trip measuring system E", false, ty(2), c17, fn(FunTransformerDiff)},
	{90, "trip I>", false, ty(2), c17, fn(FunOvercurrent)},
	{91, "trip I>>", false, ty(2), c17, fn(FunOvercurrent)},
	{92, "trip IN>", false, ty(2), c17, fn(FunOvercurrent)},
	{93, "trip IN>>", false, ty(2), c17, fn(FunOvercurrent)},
	// Table 13: auto-reclosure indications.
	{128, "CB 'on' by AR", false, ty(1), c17, fDOL},
	{129, "CB 'on' by long-time AR", false, ty(1), c17, fDOL},
	{130, "AR blocked", true, ty(1), c179, fDOL},
	// Table 14: measurands.
	{144, "measurand I", false, ty(3), c27, fDO},
	{145, "measurands I, V", false, ty(3), c27, fDO},
	{146, "measurands I, V, P, Q", false, ty(3), c27, fD},
	{147, "measurands IN, VEN", false, ty(3), c27, fDO},
	{148, "measurands IL1,2,3, VL1,2,3, P, Q, f", false, ty(9), c27, fD},
	// Table 15: generic functions.
	{240, "read headings of all defined groups", false, ty(10), co(42, 43), gen},
	{241, "read values or attributes of all entries of one group", false, ty(10), co(42, 43), gen},
	{243, "read directory of a single entry", false, ty(11), co(42, 43), gen},
	{244, "read value or attribute of a single entry", true, ty(10), co(1, 2, 7, 9, 11, 12, 42, 43), gen},
	{245, "end of general interrogation of generic data", false, ty(10), co(10), gen},
	{249, "write entry with confirmation", false, ty(10), co(41, 44), gen},
	{250, "write entry with execution", false, ty(10), co(40, 41), gen},
	{251, "write entry aborted", false, ty(10), co(40), gen},
}

// controlInfo is tables 16 to 18 (control direction).
var controlInfo = []InfoSpec{
	// Table 16: system functions.
	{0, "initiation of general interrogation", false, ty(7), co(9), glb},
	{0, "time synchronization", false, ty(6), co(8), glb},
	// Table 17: general commands.
	{16, "auto-recloser on/off", false, ty(20), co(20), fDOL},
	{17, "teleprotection on/off", false, ty(20), co(20), fDO},
	{18, "protection on/off", false, ty(20), co(20), fDOTL},
	{19, "LED reset", false, ty(20), co(20), fDOTL},
	{23, "activate characteristic 1", false, ty(20), co(20), fD},
	{24, "activate characteristic 2", false, ty(20), co(20), fD},
	{25, "activate characteristic 3", false, ty(20), co(20), fD},
	{26, "activate characteristic 4", false, ty(20), co(20), fD},
	// Table 18: generic functions.
	{240, "read headings of all defined groups", false, ty(21), co(42), gen},
	{241, "read values or attributes of all entries of one group", false, ty(21), co(42), gen},
	{243, "read directory of a single entry", false, ty(21), co(42), gen},
	{244, "read value or attribute of a single entry", false, ty(21), co(42), gen},
	{245, "general interrogation of generic data", false, ty(21), co(9), gen},
	{248, "write entry", false, ty(10), co(40), gen},
	{249, "write entry with confirmation", false, ty(10), co(40), gen},
	{250, "write entry with execution", false, ty(10), co(40), gen},
	{251, "write entry abort", false, ty(10), co(40), gen},
}

// Information numbers of the system and generic functions.
const (
	InfGIEnd             InfoNumber = 0   // GLB: end of GI (monitor) / GI initiation (control)
	InfTimeSync          InfoNumber = 0   // GLB: time synchronization
	InfResetFCB          InfoNumber = 2   // reset FCB (ASDU 5, COT 3)
	InfResetCU           InfoNumber = 3   // reset CU (ASDU 5, COT 4)
	InfStartRestart      InfoNumber = 4   // start/restart (ASDU 5, COT 5)
	InfPowerOn           InfoNumber = 5   // power on (ASDU 5, COT 6)
	InfReadHeadings      InfoNumber = 240 // GEN: read headings of all defined groups
	InfReadGroup         InfoNumber = 241 // GEN: read values or attributes of all entries of one group
	InfReadDirectory     InfoNumber = 243 // GEN: read directory of a single entry
	InfReadEntry         InfoNumber = 244 // GEN: read value or attribute of a single entry
	InfGenericGI         InfoNumber = 245 // GEN: general interrogation of generic data / its end
	InfWriteEntry        InfoNumber = 248 // GEN: write entry
	InfWriteConfirm      InfoNumber = 249 // GEN: write entry with confirmation
	InfWriteExecute      InfoNumber = 250 // GEN: write entry with execution
	InfWriteAbort        InfoNumber = 251 // GEN: write entry abort
	InfMeasurandsI       InfoNumber = 144 // measurand I (ASDU 3.1)
	InfMeasurandsIV      InfoNumber = 145 // measurands I, V (ASDU 3.2)
	InfMeasurandsIVPQ    InfoNumber = 146 // measurands I, V, P, Q (ASDU 3.3)
	InfMeasurandsINVEN   InfoNumber = 147 // measurands IN, VEN (ASDU 3.4)
	InfMeasurandsIVPQf   InfoNumber = 148 // measurands IL1,2,3, VL1,2,3, P, Q, f (ASDU 9)
	InfLEDReset          InfoNumber = 19  // LED reset
	InfAutoRecloser      InfoNumber = 16  // auto-recloser active / on/off
	InfGeneralStart      InfoNumber = 84  // general start/pick-up
	InfGeneralTrip       InfoNumber = 68  // general trip
	InfFaultLocation     InfoNumber = 73  // fault location X in ohms (ASDU 4)
	InfProtectionActive  InfoNumber = 18  // protection active / protection on/off
	InfMonitorBlocked    InfoNumber = 20  // monitor direction blocked
	InfTestMode          InfoNumber = 21  // test mode
	InfLocalParameterSet InfoNumber = 22  // local parameter setting
)

// StandardInfo returns the entries of tables 8 to 18 for inf in direction
// dir. Information number 0 has two entries in each direction (general
// interrogation and time synchronization).
func StandardInfo(dir Direction, inf InfoNumber) []InfoSpec {
	tab := monitorInfo
	if dir == Control {
		tab = controlInfo
	}
	var out []InfoSpec
	for _, s := range tab {
		if s.Inf == inf {
			out = append(out, s)
		}
	}
	return out
}

// Catalog maps function type and information number to their semantics.
// It holds the standard tables and the entries an application adds for
// private function types or information numbers. A Catalog is safe for
// concurrent use; the zero value holds the standard tables only.
type Catalog struct {
	mu sync.RWMutex
	m  map[catalogKey]InfoSpec
}

type catalogKey struct {
	dir Direction
	fun FunctionType
	inf InfoNumber
	typ TypeID
}

// Add records spec for function type fun in direction dir, for every type
// identification of spec.Types (any type when empty). It takes precedence
// over the standard tables.
func (c *Catalog) Add(dir Direction, fun FunctionType, spec InfoSpec) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[catalogKey]InfoSpec{}
	}
	types := spec.Types
	if len(types) == 0 {
		types = []TypeID{0}
	}
	for _, t := range types {
		c.m[catalogKey{dir, fun, spec.Inf, t}] = spec
	}
}

// Lookup returns the semantics of (fun, inf) in an ASDU of type typ: an
// entry added with [Catalog.Add], else, for a function type of a compatible
// range, the matching entry of tables 8 to 18. A private function type has
// no standard semantics.
func (c *Catalog) Lookup(dir Direction, typ TypeID, fun FunctionType, inf InfoNumber) (InfoSpec, bool) {
	c.mu.RLock()
	for _, t := range []TypeID{typ, 0} {
		if s, ok := c.m[catalogKey{dir, fun, inf, t}]; ok {
			c.mu.RUnlock()
			return s, true
		}
	}
	c.mu.RUnlock()
	if !fun.Compatible() {
		return InfoSpec{}, false
	}
	for _, s := range StandardInfo(dir, inf) {
		if !slices.Contains(s.Types, typ) {
			continue
		}
		// GLB and GEN are mandatory where the table lists them.
		if (slices.Contains(s.Funs, FunGlobal) || slices.Contains(s.Funs, FunGeneric)) && !slices.Contains(s.Funs, fun) {
			continue
		}
		return s, true
	}
	return InfoSpec{}, false
}

// CommandAllowed reports whether a general command (ASDU 20) with double
// command dco may be sent with (fun, inf): DCO is OFF or ON (clause
// 7.2.6.4: 0 and 3 are not used), and for a compatible function type the
// COM column of table 17 allows only ON for INF 19 (LED reset) and 23 to
// 26 (activate characteristic 1 to 4).
func CommandAllowed(fun FunctionType, inf InfoNumber, dco DPI) bool {
	if dco != DPIOff && dco != DPIOn {
		return false
	}
	if !fun.Compatible() {
		return true
	}
	switch inf {
	case InfLEDReset, 23, 24, 25, 26:
		return dco == DPIOn
	default:
		return true
	}
}
