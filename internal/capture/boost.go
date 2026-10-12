package capture

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// coreInfo is one physical core as the OS reports it: the logical processors it
// owns and its relative performance class (higher is faster; Intel hybrid parts
// report P-cores one class above E-cores, homogeneous CPUs report one class).
type coreInfo struct {
	Mask            uint64
	EfficiencyClass uint8
}

// performanceCoreMask is the affinity mask that keeps a process on the fastest
// class of cores only. It is 0 when every core is the same class: on a
// homogeneous CPU an affinity change could only take cores away, so callers
// treat 0 as "leave the affinity alone".
func performanceCoreMask(cores []coreInfo) uint64 {
	if len(cores) == 0 {
		return 0
	}
	best := cores[0].EfficiencyClass
	for _, c := range cores[1:] {
		if c.EfficiencyClass > best {
			best = c.EfficiencyClass
		}
	}
	var mask, all uint64
	for _, c := range cores {
		all |= c.Mask
		if c.EfficiencyClass == best {
			mask |= c.Mask
		}
	}
	if mask == all {
		return 0
	}
	return mask
}

// parseCores decodes the RelationProcessorCore records that
// GetLogicalProcessorInformationEx returns. Each record is
// SYSTEM_LOGICAL_PROCESSOR_INFORMATION_EX: Relationship u32, Size u32, then
// PROCESSOR_RELATIONSHIP {Flags u8, EfficiencyClass u8, Reserved [20]u8,
// GroupCount u16, GroupMask []GROUP_AFFINITY{Mask u64, Group u16, Reserved [3]u16}}.
// Cores in processor groups other than 0 cannot be expressed in a single
// affinity mask, so they are reported through the multiGroup flag instead.
func parseCores(buf []byte) (cores []coreInfo, multiGroup bool, err error) {
	const (
		relationProcessorCore = 0
		headerSize            = 8
		coreRecordSize        = headerSize + 24 + 16 // header + PROCESSOR_RELATIONSHIP fixed part + one GROUP_AFFINITY
	)
	for len(buf) > 0 {
		if len(buf) < headerSize {
			return nil, false, fmt.Errorf("truncated processor record header (%d bytes)", len(buf))
		}
		relationship := binary.LittleEndian.Uint32(buf[0:4])
		size := int(binary.LittleEndian.Uint32(buf[4:8]))
		if size < headerSize || size > len(buf) {
			return nil, false, fmt.Errorf("bad processor record size %d of %d", size, len(buf))
		}
		if relationship == relationProcessorCore {
			if size < coreRecordSize {
				return nil, false, fmt.Errorf("core record too short: %d", size)
			}
			efficiency := buf[9]
			groupCount := binary.LittleEndian.Uint16(buf[30:32])
			mask := binary.LittleEndian.Uint64(buf[32:40])
			group := binary.LittleEndian.Uint16(buf[40:42])
			if groupCount != 1 || group != 0 {
				multiGroup = true
			} else {
				cores = append(cores, coreInfo{Mask: mask, EfficiencyClass: efficiency})
			}
		}
		buf = buf[size:]
	}
	return cores, multiGroup, nil
}

// cpuList renders an affinity mask the way Task Manager does: "0-15" or "0,2,4".
func cpuList(mask uint64) string {
	var parts []string
	for i := 0; i < 64; {
		if mask&(1<<uint(i)) == 0 {
			i++
			continue
		}
		start := i
		for i < 64 && mask&(1<<uint(i)) != 0 {
			i++
		}
		if end := i - 1; end == start {
			parts = append(parts, fmt.Sprint(start))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", start, end))
		}
	}
	return strings.Join(parts, ",")
}

// GameBoostState is what the UI shows for the game-process boost.
type GameBoostState struct {
	Enabled bool   `json:"enabled"`
	Applied bool   `json:"applied"`
	PID     uint32 `json:"pid"`
	Detail  string `json:"detail"`
}
