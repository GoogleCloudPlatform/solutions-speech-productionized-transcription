package main

import (
	"log"
	"net"
	b64 "encoding/base64"
)

func main() {
	l, err := net.Listen("tcp", ":8000")
	if err != nil {
		log.Fatal(err)
	}

	defer l.Close()

	for {
		conn, err := l.Accept()
		if err != nil {
			log.Fatal(err)
		}

		go func(c net.Conn) {
			defer func() {
				c.Close()
				log.Println("defer called")
			}()
			
			b := make([]byte, 20)
			
			if _, err := c.Read(b); err != nil {
				log.Fatal(err)
			} else {
				sDec, _ := b64.StdEncoding.DecodeString(string(b))
				log.Println(string(sDec))
			}
		}(conn)
	}
}
