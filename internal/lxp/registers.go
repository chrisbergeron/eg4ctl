package lxp

import "fmt"

// EXPERIMENTAL: field offsets follow lxp-bridge's ReadInput1 packing for
// LuxPower-family inverters. They have NOT yet been verified against a live
// FlexBoss21 (tracked as CB-2); until then `status` output labels every
// derived value as experimental and always includes the raw register dump.
// Authoritative maps to cross-check: joyfulhouse/eg4_web_monitor.

// InputBank0 is a partial decode of input registers 0..N (bank 0).
type InputBank0 struct {
	Status      uint16
	VPV1        float64 // V
	VPV2        float64 // V
	VPV3        float64 // V
	VBat        float64 // V
	SOC         uint8   // %
	SOH         uint8   // %
	PPV1        uint16  // W
	PPV2        uint16  // W
	PPV3        uint16  // W
	PCharge     uint16  // W
	PDischarge  uint16  // W
}

// DecodeInputBank0 decodes the leading fields of an input-register bank 0
// response. Requires at least 24 bytes (12 registers).
func DecodeInputBank0(vals []byte) (*InputBank0, error) {
	if len(vals) < 24 {
		return nil, fmt.Errorf("need >=24 bytes of register data, got %d", len(vals))
	}
	u16 := func(off int) uint16 { return uint16(vals[off]) | uint16(vals[off+1])<<8 }
	return &InputBank0{
		Status:     u16(0),
		VPV1:       float64(u16(2)) / 10,
		VPV2:       float64(u16(4)) / 10,
		VPV3:       float64(u16(6)) / 10,
		VBat:       float64(u16(8)) / 10,
		SOC:        vals[10],
		SOH:        vals[11],
		PPV1:       u16(12),
		PPV2:       u16(14),
		PPV3:       u16(16),
		PCharge:    u16(18),
		PDischarge: u16(20),
	}, nil
}
