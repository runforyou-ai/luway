//go:build ios

package main

import (
	"C"
)

// WailsIOSMain 在 UIKit 启动后调用应用主入口。
//
//export WailsIOSMain
func WailsIOSMain() {
	main()
}
