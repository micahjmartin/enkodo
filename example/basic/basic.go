package main

import (
	"bytes"
	"log"

	"github.com/micahjmartin/enkodo"
)

func main() {
	var (
		// Original user struct
		u User
		// New user struct (will be used to copy values to)
		nu  User
		err error
	)

	u.Email = "johndoe@gmail.com"
	u.Age = 46
	u.Twitter = "@johndoe"

	buffer := bytes.NewBuffer(nil)
	// Create a writer
	w := enkodo.NewWriter(buffer)
	// Encode user to buffer
	if err = w.Encode(&u); err != nil {
		log.Fatalf("Error encoding: %v", err)
	}

	// Decode new user from buffer
	if err = enkodo.Unmarshal(buffer.Bytes(), &nu); err != nil {
		log.Fatalf("Error decoding: %v", err)
	}

	log.Printf("New user: %v", nu)
}

type SocialMedia string

// User holds the basic information for a user
type User struct {
	Email   string      `enkodo:""`
	Age     uint8       `enkodo:""`
	Twitter SocialMedia `enkodo:"string"`
}

type Post struct {
	Name    string  `enkodo:""`
	User    *User   `enkodo:""`
	Data    []byte  `enkodo:""`
	Numbers []int64 `enkodo:""`
	Users   []*User `enkodo:""`
}
