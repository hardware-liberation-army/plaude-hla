package ble

import (
	"fmt"
	"strings"
)

const (
	ServicePlaudStr      = "00001910-0000-1000-8000-00805f9b34fb"
	CharNotifyStr        = "00002bb0-0000-1000-8000-00805f9b34fb"
	CharWriteStr         = "00002bb1-0000-1000-8000-00805f9b34fb"
	ServiceBatteryStr    = "0000180f-0000-1000-8000-00805f9b34fb"
	CharBatteryLevelStr  = "00002a19-0000-1000-8000-00805f9b34fb"

	MTUTarget = 255
)

func IsPlaudService(uuid string) bool {
	return strings.EqualFold(uuid, ServicePlaudStr)
}

func IsPlaudDevice(name string, serviceUUIDs []string) bool {
	if strings.Contains(strings.ToLower(name), "plaud") {
		return true
	}
	for _, uuid := range serviceUUIDs {
		if IsPlaudService(uuid) {
			return true
		}
	}
	return false
}

func FormatUUID(uuid [16]byte) string {
	return fmt.Sprintf("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		uuid[3], uuid[2], uuid[1], uuid[0],
		uuid[5], uuid[4],
		uuid[7], uuid[6],
		uuid[8], uuid[9],
		uuid[10], uuid[11], uuid[12], uuid[13], uuid[14], uuid[15])
}
