//go:build cgo && !amrwb_static

package amrnb

// #cgo LDFLAGS: -l:libopencore-amrnb.so.0
import "C"
