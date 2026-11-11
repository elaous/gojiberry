package main

import (
	"fmt"
	"log"
)

func main() {
	addr := ":8098"
	fmt.Println("listening on", addr)
	log.Fatal(newRouter().Start(addr))
}
