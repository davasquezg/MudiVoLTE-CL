//go:build cgo && amrwb_static

package amrnb

// #cgo LDFLAGS: -lopencore-amrnb -lm
import "C"
