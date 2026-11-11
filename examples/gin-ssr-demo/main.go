package main

import (
	"fmt"
	"log"
)

func main() {
	addr := ":8097"
	fmt.Println("listening on", addr)
	log.Fatal(newRouter().Run(addr))
}
