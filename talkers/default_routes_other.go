//go:build !linux

package talkers

func defaultRouteInterfaceIndexes() (map[int]bool, error) {
	return nil, nil
}
