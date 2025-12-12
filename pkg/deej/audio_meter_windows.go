// +build windows

package deej

import (
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
)

// IAudioMeterInformation represents a peak meter on an audio stream
// https://learn.microsoft.com/en-us/windows/win32/api/endpointvolume/nn-endpointvolume-iaudiometerinformation
type IAudioMeterInformation struct {
	ole.IUnknown
}

type IAudioMeterInformationVtbl struct {
	ole.IUnknownVtbl
	GetPeakValue            uintptr
	GetMeteringChannelCount uintptr
	GetChannelsPeakValues   uintptr
	QueryHardwareSupport    uintptr
}

func (v *IAudioMeterInformation) VTable() *IAudioMeterInformationVtbl {
	return (*IAudioMeterInformationVtbl)(unsafe.Pointer(v.RawVTable))
}

// GetPeakValue gets the peak sample value for the channels in the audio stream.
// Returns a value in the normalized range from 0.0 to 1.0.
func (v *IAudioMeterInformation) GetPeakValue() (float32, error) {
	var peak float32

	hr, _, _ := syscall.Syscall(
		v.VTable().GetPeakValue,
		2,
		uintptr(unsafe.Pointer(v)),
		uintptr(unsafe.Pointer(&peak)),
		0)

	if hr != 0 {
		return 0, ole.NewError(hr)
	}

	return peak, nil
}

// IID_IAudioMeterInformation is the GUID for IAudioMeterInformation interface
var IID_IAudioMeterInformation = ole.NewGUID("{C02216F6-8C67-4B5B-9D00-D008E73E0064}")
