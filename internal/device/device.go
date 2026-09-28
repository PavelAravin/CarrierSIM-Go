package device

import (
	"fmt"
	"time"

	"github.com/danielpaulus/go-ios/ios"
)

type Info struct {
	ProductType     string
	HardwareModel   string
	ProductVersion  string
	BuildVersion    string
	ActivationState string
	Carriers        []map[string]interface{}
	RawCarriers     []map[string]interface{}
}

func ListUSBDevices() ([]string, error) {
	list, err := ios.ListDevices()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range list.DeviceList {
		if d.Properties.ConnectionType == "" || d.Properties.ConnectionType == "USB" {
			out = append(out, d.Properties.SerialNumber)
		}
	}
	return out, nil
}

func ChooseDevice(udid string, waitSeconds int) (string, error) {
	deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
	announced := false
	for {
		devices, err := ListUSBDevices()
		if err != nil {
			devices = nil
		}
		if udid != "" {
			for _, d := range devices {
				if d == udid {
					return udid, nil
				}
			}
		} else if len(devices) == 1 {
			return devices[0], nil
		}
		if udid == "" && len(devices) >= 2 {
			return "", fmt.Errorf("Подключено несколько iPhone. Укажите --udid.")
		}
		if !announced {
			fmt.Println("Ожидаю подключения iPhone по USB. Подключите и разблокируйте телефон…")
			announced = true
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("Время ожидания подключения истекло. Проверьте кабель и повторите.")
		}
		time.Sleep(2 * time.Second)
	}
}

func GetEntry(udid string) (ios.DeviceEntry, error) {
	return ios.GetDevice(udid)
}

func GetInfo(entry ios.DeviceEntry) (Info, error) {
	all, err := ios.GetValues(entry)
	if err != nil {
		return Info{}, err
	}
	v := all.Value
	info := Info{
		ProductType:     v.ProductType,
		HardwareModel:   v.HardwareModel,
		ProductVersion:  v.ProductVersion,
		BuildVersion:    v.BuildVersion,
		ActivationState: v.ActivationState,
	}
	for _, row := range v.CarrierBundleInfoArray {
		m, ok := row.(map[string]interface{})
		if !ok {
			continue
		}
		info.RawCarriers = append(info.RawCarriers, m)
		slim := map[string]interface{}{}
		for _, k := range []string{"MCC", "MNC", "Slot", "CFBundleIdentifier", "CFBundleVersion", "InternationalMobileSubscriberIdentity"} {
			if val, ok := m[k]; ok {
				slim[k] = val
			}
		}
		info.Carriers = append(info.Carriers, slim)
	}
	return info, nil
}

func ReadyDevice(udid string, waitSeconds int) (ios.DeviceEntry, error) {
	deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
	var last error
	for {
		remaining := int(time.Until(deadline).Seconds())
		if remaining < 0 {
			remaining = 0
		}
		chosen, err := ChooseDevice(udid, remaining)
		if err != nil {
			return ios.DeviceEntry{}, err
		}
		entry, err := GetEntry(chosen)
		if err == nil {
			_, err = GetInfo(entry)
		}
		if err == nil {
			return entry, nil
		}
		if last == nil {
			fmt.Println("Ожидаю разблокировки, доверия и готовности USB-соединения…")
			fmt.Println("  (", err, ")")
		}
		last = err
		if time.Now().After(deadline) {
			return ios.DeviceEntry{}, fmt.Errorf("iPhone не готов: разблокируйте и подтвердите доверие. (%v)", last)
		}
		time.Sleep(2 * time.Second)
	}
}

func AsMap(info Info) map[string]interface{} {
	return map[string]interface{}{
		"ProductType":     info.ProductType,
		"HardwareModel":   info.HardwareModel,
		"ProductVersion":  info.ProductVersion,
		"BuildVersion":    info.BuildVersion,
		"ActivationState": info.ActivationState,
		"carriers":        info.Carriers,
	}
}

func InfoStrings(info Info) map[string]string {
	return map[string]string{
		"ProductType":     info.ProductType,
		"HardwareModel":   info.HardwareModel,
		"ProductVersion":  info.ProductVersion,
		"BuildVersion":    info.BuildVersion,
		"ActivationState": info.ActivationState,
	}
}
