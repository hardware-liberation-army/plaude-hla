package protocol

import (
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

const (
	CmdHandshake       uint16 = 1
	CmdGetSSN          uint16 = 2
	CmdDeviceStatus    uint16 = 3
	CmdSyncTime        uint16 = 4
	CmdDisconnect      uint16 = 5
	CmdRoute           uint16 = 8
	CmdGetBattery      uint16 = 9
	CmdHandshakeCont   uint16 = 10
	CmdRecordStart     uint16 = 20
	CmdRecordStop      uint16 = 23
	CmdGetRecSessions  uint16 = 26
	CmdFileSyncStart   uint16 = 28
	CmdFileSyncStop    uint16 = 29
	CmdFileSyncDel     uint16 = 30
	CmdStorageInfo     uint16 = 33
	CmdFormatStorage   uint16 = 34
	CmdSyncStatistics  uint16 = 61
	CmdClearRecFile    uint16 = 104
	CmdFileInfoSync    uint16 = 112
	CmdWifiSwitch      uint16 = 103
	CmdSettingChange   uint16 = 109
	CmdOTAPush         uint16 = 143
	CmdOTAData         uint16 = 144
	CmdSNSignature     uint16 = 65040
	CmdRSAKeySend      uint16 = 65042
)

func BuildHandshake(bindToken string, subType uint8, portVersion int) []byte {
	buf := make([]byte, 255)
	off := 0
	buf[off] = 0x01
	off++
	binary.LittleEndian.PutUint16(buf[off:], 0x0001)
	off += 2
	buf[off] = 0x02
	off++
	buf[off] = subType
	off++
	if portVersion >= 3 {
		buf[off] = 0
		off++
	}
	tokenLen := 16
	if portVersion >= 9 {
		tokenLen = 32
	}
	padded := bindToken
	for len(padded) < tokenLen {
		padded += "0"
	}
	if len(padded) > tokenLen {
		padded = padded[:tokenLen]
	}
	copy(buf[off:], []byte(padded))
	off += tokenLen

	return trimTrailingZeroPairs(buf[:off])
}

func trimTrailingZeroPairs(data []byte) []byte {
	length := len(data)
	for length > 1 {
		if data[length-1] == 0 && data[length-2] == 0 {
			length--
		} else {
			break
		}
	}
	if length == 0 {
		return nil
	}
	return data[:length]
}

func BuildSyncTime() []byte {
	ts := uint32(time.Now().Unix())
	payload := make([]byte, 6)
	binary.LittleEndian.PutUint32(payload[0:4], ts)
	payload[4] = 0
	payload[5] = 0
	return BuildWithHeader(CmdSyncTime, payload)
}

func BuildDeviceStatus() []byte {
	return BuildWithHeader(CmdDeviceStatus, nil)
}

func BuildStorageInfo() []byte {
	return BuildWithHeader(CmdStorageInfo, nil)
}

func BuildRecordStart() []byte {
	return BuildWithHeader(CmdRecordStart, nil)
}

func BuildRecordStop() []byte {
	return BuildWithHeader(CmdRecordStop, nil)
}

func BuildDisconnect() []byte {
	return BuildWithHeader(CmdDisconnect, []byte{0x00})
}

func BuildDePair() []byte {
	return BuildWithHeader(CmdGetSSN, nil)
}

func BuildFormatStorage() []byte {
	return BuildWithHeader(CmdFormatStorage, []byte{0x01})
}

func BuildRaw(cmdID uint16, payload []byte) []byte {
	return BuildWithHeader(cmdID, payload)
}

func BuildCmd8Sub(subcmd uint8) []byte {
	payload := []byte{0x01, subcmd, 0x00}
	return BuildWithHeader(CmdRoute, payload)
}

func BuildGetRecSessions(sessionID uint32, uid uint32, fullSync bool) []byte {
	payload := make([]byte, 9)
	binary.LittleEndian.PutUint32(payload[0:4], sessionID)
	binary.LittleEndian.PutUint32(payload[4:8], uid)
	if fullSync {
		payload[8] = 1
	}
	return BuildWithHeader(CmdGetRecSessions, payload)
}

func BuildSyncFileStart(sessionID uint32, uid uint32, fileSize uint32) []byte {
	payload := make([]byte, 12)
	binary.LittleEndian.PutUint32(payload[0:4], sessionID)
	binary.LittleEndian.PutUint32(payload[4:8], uid)
	binary.LittleEndian.PutUint32(payload[8:12], fileSize)
	return BuildWithHeader(CmdFileSyncStart, payload)
}

func BuildSyncFileStop() []byte {
	return BuildWithHeader(CmdFileSyncStop, nil)
}

func BuildSyncFileDel(sessionID uint32) []byte {
	payload := PackUint32(sessionID)
	return BuildWithHeader(CmdFileSyncDel, payload)
}

func BuildClearRecFile() []byte {
	return BuildWithHeader(CmdClearRecFile, nil)
}

func BuildChunkedPackets(cmdID uint16, data []byte, chunkSize int) [][]byte {
	totalChunks := (len(data) + chunkSize - 1) / chunkSize
	packets := make([][]byte, 0, totalChunks)
	for i := 0; i < totalChunks; i++ {
		off := i * chunkSize
		end := off + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[off:end]
		pkt := make([]byte, 4+len(chunk))
		binary.LittleEndian.PutUint16(pkt[0:2], cmdID)
		pkt[2] = byte(totalChunks)
		pkt[3] = byte(i)
		copy(pkt[4:], chunk)
		packets = append(packets, pkt)
	}
	return packets
}

func BuildSNSignaturePackets(sigData []byte) [][]byte {
	return BuildChunkedPackets(CmdSNSignature, sigData, 100)
}

func BuildRSAPublicKeyPackets(keyData []byte) [][]byte {
	return BuildChunkedPackets(CmdRSAKeySend, keyData, 100)
}

func ReassembleChunks(notifications [][]byte, targetCmdID uint16) []byte {
	chunks := make(map[int][]byte)
	for _, n := range notifications {
		if len(n) < 4 {
			continue
		}
		cmdID := binary.LittleEndian.Uint16(n[0:2])
		if cmdID != targetCmdID {
			continue
		}
		total := int(n[2])
		idx := int(n[3])
		if idx < total {
			chunks[idx] = n[4:]
		}
	}
	if len(chunks) == 0 {
		return nil
	}
	result := make([]byte, 0)
	maxIdx := 0
	for k := range chunks {
		if k > maxIdx {
			maxIdx = k
		}
	}
	for i := 0; i <= maxIdx; i++ {
		if c, ok := chunks[i]; ok {
			result = append(result, c...)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func CmdName(cmdID uint16) string {
	names := map[uint16]string{
		1:     "HANDSHAKE",
		2:     "GET_SSN",
		3:     "DEVICE_STATUS",
		4:     "SYNC_TIME",
		5:     "DISCONNECT",
		8:     "CMD_ROUTE",
		9:     "GET_BATTERY",
		10:    "HANDSHAKE_CONTINUE",
		20:    "RECORD_START",
		23:    "RECORD_STOP",
		26:    "GET_REC_SESSIONS",
		28:    "FILE_SYNC_START",
		29:    "FILE_SYNC_STOP",
		30:    "FILE_SYNC_DEL",
		33:    "STORAGE_INFO",
		34:    "FORMAT_STORAGE",
		61:    "SYNC_STATISTICS",
		104:   "CLEAR_REC_FILE",
		108:   "DEVICE_NAME",
		112:   "FILE_INFO_SYNC",
		141:   "ADV_SETTING_141",
		143:   "OTA_PUSH",
		144:   "OTA_DATA",
		65040: "SN_SIGNATURE",
		65041: "SN_SIGNATURE_RESP",
		65042: "RSA_KEY_SEND",
	}
	if name, ok := names[cmdID]; ok {
		return name
	}
	return fmt.Sprintf("UNKNOWN_0x%04X", cmdID)
}

func GetJWTSubject(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid JWT format")
	}
	payload := parts[1]
	for len(payload)%4 != 0 {
		payload += "="
	}
	return string(payload), nil
}

func GetInitSequence() []struct {
	Label   string
	Builder func() []byte
} {
	return []struct {
		Label   string
		Builder func() []byte
	}{
		{"Sync time", BuildSyncTime},
		{"Battery (cmd 9)", func() []byte { return BuildRaw(9, nil) }},
		{"Device status", BuildDeviceStatus},
		{"cmd 8 sub 0x0F", func() []byte { return BuildCmd8Sub(0x0F) }},
		{"cmd 8 sub 0x11", func() []byte { return BuildCmd8Sub(0x11) }},
		{"Device name (cmd 108)", func() []byte { return BuildRaw(108, nil) }},
		{"cmd 141", func() []byte { return BuildRaw(141, nil) }},
		{"Device name (cmd 108) again", func() []byte { return BuildRaw(108, nil) }},
		{"File sync init (cmd 25)", func() []byte { return BuildRaw(25, []byte{0x00}) }},
		{"Device status", BuildDeviceStatus},
		{"cmd 8 sub 0x17", func() []byte { return BuildCmd8Sub(0x17) }},
		{"cmd 24", func() []byte { return BuildRaw(24, nil) }},
		{"cmd 8 sub 0x14", func() []byte { return BuildCmd8Sub(0x14) }},
		{"cmd 8 sub 0x13", func() []byte { return BuildCmd8Sub(0x13) }},
		{"cmd 8 sub 0x18", func() []byte { return BuildCmd8Sub(0x18) }},
		{"cmd 8 sub 0x19", func() []byte { return BuildCmd8Sub(0x19) }},
		{"cmd 8 sub 0x1F", func() []byte { return BuildCmd8Sub(0x1F) }},
		{"cmd 8 sub 0x1B", func() []byte { return BuildCmd8Sub(0x1B) }},
		{"cmd 8 sub 0x20", func() []byte { return BuildCmd8Sub(0x20) }},
		{"cmd 6 (sync statistics)", func() []byte { return BuildRaw(6, nil) }},
	}
}
