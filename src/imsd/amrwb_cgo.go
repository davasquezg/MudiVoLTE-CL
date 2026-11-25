//go:build cgo

package main

// AMR-WB and AMR on the network side exist only in the cgo build
// (opencore-amrwb + vo-amrwbenc, opencore-amrnb). Without cgo the daemon
// negotiates G.711.
import (
	_ "github.com/xarvel/MudiVoLTE/volte/media/amrnb"
	_ "github.com/xarvel/MudiVoLTE/volte/media/amrwb"
)
