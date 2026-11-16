//go:build !linux && !darwin && !windows

package dragout

func reveal(string) error {
	return ErrUnsupported
}
