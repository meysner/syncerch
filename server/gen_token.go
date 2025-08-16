package main

import (
	"crypto/rand"
	"fmt"
	"os"
)

const tokenFile = "./tokens.txt"

func main() {
	token := generateToken(64)
	f, _ := os.OpenFile(tokenFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	defer f.Close()
	f.WriteString(token + "\n")
	fmt.Println("New token:", token)
}

func generateToken(length int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	bytes := make([]byte, length)
	rand.Read(bytes)
	for i, b := range bytes {
		bytes[i] = letters[b%byte(len(letters))]
	}
	return string(bytes)
}
