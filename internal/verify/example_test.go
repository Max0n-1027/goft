package verify_test

import (
	"fmt"
	"strings"

	"goft/internal/verify"
)

// Hashing a stream is how goft records what it sent: the same digest is
// computed again from the destination and the two are compared.
func ExampleHash() {
	sent, err := verify.Hash(strings.NewReader("hello"))
	if err != nil {
		panic(err)
	}
	readBack, err := verify.Hash(strings.NewReader("hello world"))
	if err != nil {
		panic(err)
	}

	fmt.Println(verify.Format(sent))
	fmt.Println(verify.Format(readBack))
	fmt.Println("intact:", sent == readBack)
	// Output:
	// 26c7827d889f6da3
	// 45ab6734b21e6968
	// intact: false
}

// Format renders a digest the way it appears in the log, so that records line
// up whatever the value.
func ExampleFormat() {
	fmt.Println(verify.Format(1))
	// Output: 0000000000000001
}
