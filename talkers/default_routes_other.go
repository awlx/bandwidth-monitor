//go:build !linux && !freebsd

package talkers

func defaultRouteInterfaceIndexes() (map[int]bool, error) {
	return nil, nil
}
