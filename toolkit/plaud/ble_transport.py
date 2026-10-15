import asyncio
import struct
from typing import Callable, Optional
from bleak import BleakClient, BleakScanner
from bleak.backends.device import BLEDevice
from bleak.backends.characteristic import BleakGATTCharacteristic

SERVICE_PLAUD = "00001910-0000-1000-8000-00805f9b34fb"
CHAR_NOTIFY = "00002bb0-0000-1000-8000-00805f9b34fb"
CHAR_WRITE = "00002bb1-0000-1000-8000-00805f9b34fb"
SERVICE_BATTERY = "0000180f-0000-1000-8000-00805f9b34fb"
CHAR_BATTERY_LEVEL = "00002a19-0000-1000-8000-00805f9b34fb"
CHAR_CCCD = "00002902-0000-1000-8000-00805f9b34fb"

MTU_TARGET = 255


class BleTransport:
    def __init__(self):
        self.client: Optional[BleakClient] = None
        self._notify_callback: Optional[Callable] = None
        self._mtu: int = 23

    @staticmethod
    async def scan(duration: float = 10.0) -> list[BLEDevice]:
        devices = await BleakScanner.discover(timeout=duration, return_adv=True)
        results = []
        for addr, (device, adv) in devices.items():
            name = adv.local_name or device.name or ""
            if "plaud" in name.lower() or "Plaud" in name:
                results.append(device)
            for uuid in adv.service_uuids:
                if uuid.lower() == SERVICE_PLAUD.lower():
                    results.append(device)
                    break
        seen = set()
        unique = []
        for d in results:
            if d.address not in seen:
                seen.add(d.address)
                unique.append(d)
        return unique

    @staticmethod
    async def scan_all(duration: float = 10.0) -> list[tuple[BLEDevice, dict]]:
        devices = await BleakScanner.discover(timeout=duration, return_adv=True)
        results = []
        for addr, (device, adv) in devices.items():
            results.append((device, {
                "name": adv.local_name or device.name or "",
                "rssi": adv.rssi,
                "service_uuids": adv.service_uuids,
                "manufacturer_data": {k: v.hex() for k, v in adv.manufacturer_data.items()},
            }))
        return sorted(results, key=lambda x: x[1]["rssi"], reverse=True)

    async def connect(self, address: str) -> bool:
        if self.client and self.client.is_connected:
            await self.disconnect()

        self.client = BleakClient(
            address,
            disconnected_callback=self._on_disconnect,
        )
        await self.client.connect()
        if not self.client.is_connected:
            return False

        try:
            await self.client.request_mtu(MTU_TARGET)
        except Exception:
            pass

        return True

    async def disconnect(self):
        if self.client and self.client.is_connected:
            try:
                await self.unsubscribe_notify()
            except Exception:
                pass
            await self.client.disconnect()
        self.client = None

    async def discover_services(self) -> dict:
        if not self.client or not self.client.is_connected:
            return {}
        services = {}
        for service in self.client.services:
            chars = []
            for char in service.characteristics:
                props = char.properties
                descs = []
                for desc in char.descriptors:
                    descs.append({
                        "uuid": desc.uuid,
                        "handle": desc.handle,
                    })
                chars.append({
                    "uuid": char.uuid,
                    "properties": props,
                    "descriptors": descs,
                })
            services[service.uuid] = {
                "description": service.description,
                "characteristics": chars,
            }
        return services

    async def read_battery(self) -> int:
        if not self.client or not self.client.is_connected:
            return -1
        data = await self.client.read_gatt_char(CHAR_BATTERY_LEVEL)
        return data[0] if data else -1

    async def subscribe_notify(self, callback: Callable[[bytes], None]):
        if not self.client or not self.client.is_connected:
            return
        self._notify_callback = callback

        def _on_notification(char: BleakGATTCharacteristic, data: bytearray):
            if self._notify_callback:
                self._notify_callback(bytes(data))

        await self.client.start_notify(CHAR_NOTIFY, _on_notification)

    async def unsubscribe_notify(self):
        if self.client and self.client.is_connected and self._notify_callback:
            try:
                await self.client.stop_notify(CHAR_NOTIFY)
            except Exception:
                pass
            self._notify_callback = None

    async def write_command(self, data: bytes, with_response: bool = True) -> bool:
        if not self.client or not self.client.is_connected:
            return False
        try:
            await self.client.write_gatt_char(CHAR_WRITE, data, response=with_response)
            return True
        except Exception as e:
            print(f"Write error: {e}")
            return False

    async def write_command_with_response(self, data: bytes) -> bool:
        if not self.client or not self.client.is_connected:
            return False
        try:
            await self.client.write_gatt_char(CHAR_WRITE, data, response=True)
            return True
        except Exception as e:
            print(f"Write error: {e}")
            return False

    async def read_characteristic(self, uuid: str) -> Optional[bytes]:
        if not self.client or not self.client.is_connected:
            return None
        try:
            return await self.client.read_gatt_char(uuid)
        except Exception as e:
            print(f"Read error: {e}")
            return None

    def _on_disconnect(self, client: BleakClient):
        print("Device disconnected")

    @property
    def is_connected(self) -> bool:
        return self.client is not None and self.client.is_connected

    @property
    def mtu(self) -> int:
        return self._mtu

    @mtu.setter
    def mtu(self, value: int):
        self._mtu = value
