//go:build !linux && !darwin

package markers

func lowPriority(int) {}

func lowerThreadPriority() {}
