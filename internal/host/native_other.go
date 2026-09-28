//go:build !windows && !darwin

package host

import "fmt"

func NativeHost(udid string, assets [][]string, directories []string) error {
	return fmt.Errorf("Поддерживаются macOS и Windows.")
}

func RunHostFromConfig(path string) error {
	return NativeHost("", nil, nil)
}
