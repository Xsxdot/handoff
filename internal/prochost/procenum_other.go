//go:build !darwin && !linux

// procenum_other.go —— 非 darwin/linux 的空实现。
//
// 一律返回 errNotSupported 而不是空集：调用方必须据此降级为「未知」，
// 而不是渲染出一个 0 让人误以为足迹是空的（见 procenum.go 的 why）。
package prochost

func enumProcs() ([]procEntry, error) { return nil, errNotSupported }

func procLimit() (int, error) { return 0, errNotSupported }
