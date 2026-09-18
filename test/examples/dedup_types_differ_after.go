//go:build ignore

// The same text in two functions, but opts has a different type in each. The
// call gopls writes for the first copy would not compile at the second.
package main

import "fmt"

type feishuOptions struct{ name, token string }

type weixinOptions struct{ name, token string }

func a(opts feishuOptions) {
	fmt.Println("configuring", opts.name)
	fmt.Println("token length", len(opts.token), opts.token != "")
	fmt.Println("done", opts.name)
}

func b(opts weixinOptions) {
	fmt.Println("configuring", opts.name)
	fmt.Println("token length", len(opts.token), opts.token != "")
	fmt.Println("done", opts.name)
}

func main() {
	a(feishuOptions{"x", "t"})
	a(feishuOptions{"y", "u"})
	b(weixinOptions{"x", "t"})
	b(weixinOptions{"y", "u"})
}
