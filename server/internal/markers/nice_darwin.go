package markers

// lowerThreadPriority does nothing on macOS, where a priority cannot be set
// on one thread; ffmpeg still runs at nice 19. macOS is for development.
func lowerThreadPriority() {}
