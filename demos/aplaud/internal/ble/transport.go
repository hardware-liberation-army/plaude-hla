package ble

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

var adapter *bluetooth.Adapter

func init() {
	var err error
	adapter = bluetooth.DefaultAdapter
	err = adapter.Enable()
	if err != nil {
		log.Fatalf("Failed to enable BLE adapter: %v", err)
	}
}

type DeviceInfo struct {
	Address bluetooth.Address
	Name    string
	RSSI    int16
}

type Transport struct {
	mu              sync.Mutex
	device          *bluetooth.Device
	notifyChar     *bluetooth.DeviceCharacteristic
	writeChar      *bluetooth.DeviceCharacteristic
	batteryChar    *bluetooth.DeviceCharacteristic
	notifyCh       chan []byte
	notifyCallback func([]byte)
	connected      bool
}

func NewTransport() *Transport {
	return &Transport{
		notifyCh: make(chan []byte, 256),
	}
}

func (t *Transport) Notifications() <-chan []byte {
	return t.notifyCh
}

func Scan(duration float64) ([]DeviceInfo, error) {
	return scanFiltered(duration, func(name string) bool {
		return IsPlaudDevice(name, nil)
	})
}

func ScanAll(duration float64) ([]DeviceInfo, error) {
	return scanFiltered(duration, func(name string) bool {
		return true
	})
}

func scanFiltered(duration float64, filter func(string) bool) ([]DeviceInfo, error) {
	adapter.StopScan()

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(1 * time.Second)
			adapter.StopScan()
		}

		devices, err := doScan(duration, filter)
		if err == nil {
			return devices, nil
		}
		lastErr = err
		if !strings.Contains(err.Error(), "already in progress") {
			return nil, err
		}
	}
	return nil, fmt.Errorf("BLE scan: %w", lastErr)
}

func doScan(duration float64, filter func(string) bool) ([]DeviceInfo, error) {
	var devices []DeviceInfo
	var mu sync.Mutex
	seen := make(map[string]bool)
	found := make(chan struct{}, 1)

	scanErr := make(chan error, 1)
	go func() {
		err := adapter.Scan(func(a *bluetooth.Adapter, result bluetooth.ScanResult) {
			name := result.LocalName()
			addrStr := result.Address.String()

			if !filter(name) {
				return
			}

			mu.Lock()
			defer mu.Unlock()
			if seen[addrStr] {
				return
			}
			seen[addrStr] = true
			devices = append(devices, DeviceInfo{
				Address: result.Address,
				Name:    name,
				RSSI:    result.RSSI,
			})
			select {
			case found <- struct{}{}:
			default:
			}
		})
		scanErr <- err
	}()

	select {
	case <-found:
	case <-time.After(time.Duration(duration * float64(time.Second))):
	}
	adapter.StopScan()

	if err := <-scanErr; err != nil {
		return nil, err
	}
	return devices, nil
}

func (t *Transport) Connect(address string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.connected {
		t.disconnectLocked()
	}

	var addr bluetooth.Address
	addr.Set(address)

	fmt.Printf("Connecting to %s...\n", address)
	device, err := adapter.Connect(addr, bluetooth.ConnectionParams{})
	if err != nil {
		return fmt.Errorf("connecting: %w", err)
	}

	t.device = &device
	t.connected = true
	fmt.Println("Connected.")

	return nil
}

func (t *Transport) DiscoverServices() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.connected || t.device == nil {
		return fmt.Errorf("not connected")
	}

	services, err := t.device.DiscoverServices(nil)
	if err != nil {
		return fmt.Errorf("discovering services: %w", err)
	}

	plaudServiceUUID, _ := bluetooth.ParseUUID(ServicePlaudStr)
	batteryServiceUUID, _ := bluetooth.ParseUUID(ServiceBatteryStr)
	notifyUUID, _ := bluetooth.ParseUUID(CharNotifyStr)
	writeUUID, _ := bluetooth.ParseUUID(CharWriteStr)
	batteryLevelUUID, _ := bluetooth.ParseUUID(CharBatteryLevelStr)

	for _, svc := range services {
		chars, err := svc.DiscoverCharacteristics(nil)
		if err != nil {
			fmt.Printf("Warning: failed to discover characteristics for %s: %v\n", svc.UUID().String(), err)
			continue
		}

		for i := range chars {
			charUUID := chars[i].UUID()
			if charUUID == notifyUUID {
				t.notifyChar = &chars[i]
			} else if charUUID == writeUUID {
				t.writeChar = &chars[i]
			} else if charUUID == batteryLevelUUID {
				t.batteryChar = &chars[i]
			}
		}

		svcUUID := svc.UUID()
		if svcUUID == plaudServiceUUID || svcUUID == batteryServiceUUID {
			fmt.Printf("  Service: %s (%d characteristics)\n", svcUUID.String(), len(chars))
		}
	}

	if t.notifyChar == nil {
		return fmt.Errorf("notify characteristic (0x2BB0) not found")
	}
	if t.writeChar == nil {
		return fmt.Errorf("write characteristic (0x2BB1) not found")
	}

	return nil
}

func (t *Transport) SubscribeNotify(callback func([]byte)) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.notifyChar == nil {
		return fmt.Errorf("notify characteristic not discovered")
	}

	t.notifyCallback = callback

	err := t.notifyChar.EnableNotifications(func(value []byte) {
		data := make([]byte, len(value))
		copy(data, value)
		if t.notifyCallback != nil {
			t.notifyCallback(data)
		}
		select {
		case t.notifyCh <- data:
		default:
		}
	})
	if err != nil {
		return fmt.Errorf("subscribing to notifications: %w", err)
	}

	fmt.Println("Subscribed to notifications.")
	return nil
}

func (t *Transport) WriteCommand(data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.writeChar == nil {
		return fmt.Errorf("write characteristic not discovered")
	}

	_, err := t.writeChar.WriteWithoutResponse(data)
	if err != nil {
		_, err2 := t.writeChar.Write(data)
		if err2 != nil {
			return fmt.Errorf("writing: %w (write-without-response: %w)", err2, err)
		}
	}

	return nil
}

func (t *Transport) ReadBattery() (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.batteryChar == nil {
		return -1, fmt.Errorf("battery characteristic not discovered")
	}

	data := make([]byte, 1)
	_, err := t.batteryChar.Read(data)
	if err != nil {
		return -1, fmt.Errorf("reading battery: %w", err)
	}
	return int(data[0]), nil
}

func (t *Transport) Disconnect() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.disconnectLocked()
}

func (t *Transport) disconnectLocked() error {
	if t.device != nil && t.connected {
		if err := t.device.Disconnect(); err != nil {
			fmt.Printf("Disconnect error: %v\n", err)
		}
	}
	t.device = nil
	t.notifyChar = nil
	t.writeChar = nil
	t.batteryChar = nil
	t.connected = false
	fmt.Println("Disconnected.")
	return nil
}

func (t *Transport) IsConnected() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connected
}

func (t *Transport) WaitForNotification(timeout time.Duration) ([]byte, error) {
	select {
	case data := <-t.notifyCh:
		return data, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for notification (%v)", timeout)
	}
}

func (t *Transport) CollectNotifications(timeout time.Duration) [][]byte {
	var results [][]byte
	deadline := time.After(timeout)
	for {
		select {
		case data := <-t.notifyCh:
			results = append(results, data)
		case <-deadline:
			return results
		}
	}
}

func FormatAddress(addr bluetooth.Address) string {
	return addr.String()
}

func FindPlaudByName(name string) bool {
	return strings.Contains(strings.ToLower(name), "plaud")
}

func StopScan() {
	adapter.StopScan()
}
