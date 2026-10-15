package protocol

import (
	"encoding/binary"
	"fmt"
)

func CRC16(data []byte, init uint16) uint16 {
	crc := init
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&0x0001 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc & 0xFFFF
}

func PackUint8(v uint8) []byte {
	return []byte{v}
}

func PackUint16(v uint16) []byte {
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, v)
	return buf
}

func PackUint32(v uint32) []byte {
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, v)
	return buf
}

func PackUint64(v uint64) []byte {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, v)
	return buf
}

func ReadUint8(data []byte, offset int) uint8 {
	if offset >= len(data) {
		return 0
	}
	return data[offset]
}

func ReadUint16(data []byte, offset int) uint16 {
	if offset+2 > len(data) {
		return 0
	}
	return binary.LittleEndian.Uint16(data[offset:])
}

func ReadUint32(data []byte, offset int) uint32 {
	if offset+4 > len(data) {
		return 0
	}
	return binary.LittleEndian.Uint32(data[offset:])
}

func ReadUint64(data []byte, offset int) uint64 {
	if offset+8 > len(data) {
		return 0
	}
	return binary.LittleEndian.Uint64(data[offset:])
}

type Packet struct {
	CmdID   uint16
	Payload []byte
}

func (p *Packet) ToBytes() []byte {
	if p.CmdID > 255 {
		buf := make([]byte, 4+len(p.Payload))
		binary.LittleEndian.PutUint16(buf[0:2], p.CmdID)
		binary.LittleEndian.PutUint16(buf[2:4], uint16(len(p.Payload)))
		copy(buf[4:], p.Payload)
		return buf
	}
	buf := make([]byte, 3+len(p.Payload))
	buf[0] = 0x01
	binary.LittleEndian.PutUint16(buf[1:3], p.CmdID)
	copy(buf[3:], p.Payload)
	return buf
}

func ParsePacket(data []byte) (*Packet, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("packet too short: %d bytes", len(data))
	}

	cmdID := ReadUint16(data, 0)
	if cmdID >= 256 {
		var payload []byte
		if len(data) >= 4 {
			payload = data[4:]
		} else {
			payload = data[2:]
		}
		return &Packet{CmdID: cmdID, Payload: payload}, nil
	}

	if len(data) < 3 {
		return &Packet{CmdID: cmdID, Payload: nil}, nil
	}

	pktType := data[0]
	switch pktType {
	case 0x01, 0x02, 0x04:
		cmdID = ReadUint16(data, 1)
		return &Packet{CmdID: cmdID, Payload: data[3:]}, nil
	case 0x05:
		cmdID = ReadUint16(data, 1)
		return &Packet{CmdID: cmdID, Payload: data[5:]}, nil
	case 0x08:
		subCmd := ReadUint16(data, 3)
		return &Packet{CmdID: subCmd, Payload: data[5:]}, nil
	default:
		cmdID = ReadUint16(data, 1)
		return &Packet{CmdID: cmdID, Payload: data[3:]}, nil
	}
}

func BuildWithHeader(cmdID uint16, payload []byte) []byte {
	buf := make([]byte, 3+len(payload))
	buf[0] = 0x01
	binary.LittleEndian.PutUint16(buf[1:3], cmdID)
	copy(buf[3:], payload)
	return buf
}

func HexDump(data []byte, width int) string {
	var lines []string
	for i := 0; i < len(data); i += width {
		end := i + width
		if end > len(data) {
			end = len(data)
		}
		chunk := data[i:end]
		hexPart := ""
		for j, b := range chunk {
			if j > 0 {
				hexPart += " "
			}
			hexPart += fmt.Sprintf("%02X", b)
		}
		asciiPart := ""
		for _, b := range chunk {
			if b >= 32 && b < 127 {
				asciiPart += string(b)
			} else {
				asciiPart += "."
			}
		}
		lines = append(lines, fmt.Sprintf("  %04X  %-*s  %s", i, width*3-1, hexPart, asciiPart))
	}
	result := ""
	for _, l := range lines {
		result += l + "\n"
	}
	return result
}
