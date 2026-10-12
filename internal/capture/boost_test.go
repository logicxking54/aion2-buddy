package capture

import (
	"encoding/binary"
	"testing"
)

// coreRecord builds one RelationProcessorCore record the way the OS lays it out.
func coreRecord(efficiency uint8, mask uint64, group uint16) []byte {
	rec := make([]byte, 48)
	binary.LittleEndian.PutUint32(rec[0:4], 0) // RelationProcessorCore
	binary.LittleEndian.PutUint32(rec[4:8], 48)
	rec[9] = efficiency
	binary.LittleEndian.PutUint16(rec[30:32], 1)
	binary.LittleEndian.PutUint64(rec[32:40], mask)
	binary.LittleEndian.PutUint16(rec[40:42], group)
	return rec
}

// A 12900K-style part: eight P-cores with two threads each on CPUs 0-15 and
// eight single-thread E-cores on 16-23. The boost must keep only 0-15.
func hybridCores() []coreInfo {
	var cores []coreInfo
	for i := 0; i < 8; i++ {
		cores = append(cores, coreInfo{Mask: 0x3 << uint(2*i), EfficiencyClass: 1})
	}
	for i := 16; i < 24; i++ {
		cores = append(cores, coreInfo{Mask: 1 << uint(i), EfficiencyClass: 0})
	}
	return cores
}

func TestPerformanceCoreMaskKeepsOnlyPCores(t *testing.T) {
	if got := performanceCoreMask(hybridCores()); got != 0xFFFF {
		t.Fatalf("mask = %#x, want 0xFFFF (CPUs 0-15)", got)
	}
}

// Restricting a CPU whose cores are all the same class would only remove cores,
// so the mask must come back as "leave it alone".
func TestPerformanceCoreMaskIsZeroOnHomogeneousCPU(t *testing.T) {
	var cores []coreInfo
	for i := 0; i < 8; i++ {
		cores = append(cores, coreInfo{Mask: 0x3 << uint(2*i), EfficiencyClass: 0})
	}
	if got := performanceCoreMask(cores); got != 0 {
		t.Fatalf("homogeneous mask = %#x, want 0", got)
	}
	if got := performanceCoreMask(nil); got != 0 {
		t.Fatalf("empty mask = %#x, want 0", got)
	}
}

func TestParseCoresReadsEfficiencyClassAndMask(t *testing.T) {
	buf := append(coreRecord(1, 0x3, 0), coreRecord(0, 0x10000, 0)...)
	// An unrelated record type (RelationCache = 2) must be skipped by its size.
	other := make([]byte, 16)
	binary.LittleEndian.PutUint32(other[0:4], 2)
	binary.LittleEndian.PutUint32(other[4:8], 16)
	buf = append(buf, other...)

	cores, multi, err := parseCores(buf)
	if err != nil {
		t.Fatal(err)
	}
	if multi {
		t.Fatal("single-group records reported as multi-group")
	}
	want := []coreInfo{{Mask: 0x3, EfficiencyClass: 1}, {Mask: 0x10000, EfficiencyClass: 0}}
	if len(cores) != len(want) {
		t.Fatalf("cores = %+v, want %+v", cores, want)
	}
	for i := range want {
		if cores[i] != want[i] {
			t.Fatalf("core %d = %+v, want %+v", i, cores[i], want[i])
		}
	}
	if got := performanceCoreMask(cores); got != 0x3 {
		t.Fatalf("mask from parsed cores = %#x, want 0x3", got)
	}
}

// A core in processor group 1 cannot be covered by one affinity mask; the parser
// must flag it rather than silently fold it into group 0's numbering.
func TestParseCoresFlagsOtherProcessorGroups(t *testing.T) {
	buf := append(coreRecord(1, 0x1, 0), coreRecord(1, 0x1, 1)...)
	cores, multi, err := parseCores(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !multi || len(cores) != 1 {
		t.Fatalf("multi=%t cores=%d, want multi=true with the group-0 core only", multi, len(cores))
	}
}

func TestParseCoresRejectsCorruptSizes(t *testing.T) {
	rec := coreRecord(1, 0x1, 0)
	binary.LittleEndian.PutUint32(rec[4:8], 4) // smaller than its own header
	if _, _, err := parseCores(rec); err == nil {
		t.Fatal("corrupt record size was accepted")
	}
}

func TestCpuListFormatsRanges(t *testing.T) {
	cases := map[uint64]string{
		0xFFFF:   "0-15",
		0x5555:   "0,2,4,6,8,10,12,14",
		0x1 << 5: "5",
		0xF00F:   "0-3,12-15",
	}
	for mask, want := range cases {
		if got := cpuList(mask); got != want {
			t.Fatalf("cpuList(%#x) = %q, want %q", mask, got, want)
		}
	}
}
