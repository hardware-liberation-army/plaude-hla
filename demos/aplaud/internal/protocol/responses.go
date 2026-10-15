package protocol

import (
	"encoding/binary"
	"fmt"
)

type BleFile struct {
	SessionID uint32
	FileSize  uint32
	Attribute uint8
	Scene     uint8
}

func (f *BleFile) String() string {
	return fmt.Sprintf("BleFile(id=%d, size=%d, attr=%d, scene=%d)",
		f.SessionID, f.FileSize, f.Attribute, f.Scene)
}

func HumanSize(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	for _, u := range units {
		if f < 1024 {
			if u == "B" {
				return fmt.Sprintf("%.0f %s", f, u)
			}
			return fmt.Sprintf("%.1f %s", f, u)
		}
		f /= 1024
	}
	return fmt.Sprintf("%.1f TB", f)
}

type FileListResponse struct {
	Files []*BleFile
	Total int
	UID   uint32
}

func ParseFileListResponse(data []byte, protocolVersion int) *FileListResponse {
	resp := &FileListResponse{}
	if len(data) < 7 {
		return resp
	}
	resp.UID = ReadUint32(data, 3)
	resp.Total = int(ReadUint16(data, 7))

	entrySize := 10
	offset := 9
	if len(data) > offset+2 {
		offset = 11
	}

	for offset+entrySize <= len(data) {
		sid := ReadUint32(data, offset)
		fsz := ReadUint32(data, offset+4)
		attr := data[offset+8]
		scene := data[offset+9]
		resp.Files = append(resp.Files, &BleFile{
			SessionID: sid,
			FileSize:  fsz,
			Attribute: attr,
			Scene:     scene,
		})
		offset += entrySize
	}
	return resp
}

type SyncFileHeadResponse struct {
	SessionID uint32
	Status    uint8
}

func (r *SyncFileHeadResponse) OK() bool {
	return r.Status == 0
}

func (r *SyncFileHeadResponse) String() string {
	return fmt.Sprintf("SyncFileHead(session=%d, status=%d)", r.SessionID, r.Status)
}

func ParseSyncFileHead(data []byte) *SyncFileHeadResponse {
	resp := &SyncFileHeadResponse{}
	if len(data) >= 7 {
		resp.SessionID = ReadUint32(data, 3)
		resp.Status = data[7]
	}
	return resp
}

type SyncFileTailResponse struct {
	SessionID uint32
	CRC       uint16
}

func (r *SyncFileTailResponse) String() string {
	return fmt.Sprintf("SyncFileTail(session=%d, crc=0x%04X)", r.SessionID, r.CRC)
}

func ParseSyncFileTail(data []byte) *SyncFileTailResponse {
	resp := &SyncFileTailResponse{}
	if len(data) >= 9 {
		resp.SessionID = ReadUint32(data, 3)
		resp.CRC = ReadUint16(data, 7)
	}
	return resp
}

type FileDataNotification struct {
	SessionID  uint32
	FileOffset uint32
	Payload    []byte
	IsLast     bool
}

func (n *FileDataNotification) String() string {
	return fmt.Sprintf("FileData(session=%d, offset=%d, %dB, last=%v)",
		n.SessionID, n.FileOffset, len(n.Payload), n.IsLast)
}

func ParseFileDataNotification(data []byte) *FileDataNotification {
	fd := &FileDataNotification{}
	if len(data) < 10 {
		return fd
	}
	fd.SessionID = binary.LittleEndian.Uint32(data[1:5])
	fd.FileOffset = binary.LittleEndian.Uint32(data[5:9])
	if fd.FileOffset == 0xFFFFFFFF {
		fd.IsLast = true
		return fd
	}
	dataLen := int(data[9])
	if dataLen > 0 {
		end := 10 + dataLen
		if end > len(data) {
			end = len(data)
		}
		fd.Payload = data[10:end]
	} else {
		fd.Payload = data[10:]
	}
	return fd
}
