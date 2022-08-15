package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	b64 "encoding/base64"
)

func main() {
	conn, err := net.Dial("tcp", "localhost:8000")
	if err != nil {
		log.Fatal(err)
	}

	data := "abc123!?$*&()'-=@~"
	sEnc := b64.StdEncoding.EncodeToString([]byte(data))

	fmt.Fprintf(conn, sEnc)
	status, err := bufio.NewReader(conn).ReadString('\n')

	log.Println(status)
}
