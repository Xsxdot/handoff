//go:build !darwin && !linux

// fence_other.go —— 非 darwin/linux 平台的围栏占位实现。
//
// 职责：让本包在所有平台可编译，并把「这个平台没有围栏」如实告诉调用方
//
// 边界：不做任何降级模拟——没有围栏就是没有，装作装上了会让调用方
// 以为有保护而放心扇出，比明确没有更危险
package prochost

// setNprocLimit 在本平台无实现。
func setNprocLimit(int) error { return errFenceNotSupported }

// getNprocLimit 在本平台无实现。
func getNprocLimit() (int, error) { return 0, errFenceNotSupported }
