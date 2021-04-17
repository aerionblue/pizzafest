// Package twitchchat handles auth credentials for Twitch chat.
package twitchchat

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
)

type Creds struct {
	Username   string `json:"username"`
	OAuthToken string `json:"oauthToken"`
}

func ParseCreds(path string) (Creds, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return Creds{}, fmt.Errorf("couldn't read Twitch chat credentials file: %v", err)
	}
	var c Creds
	if err := json.Unmarshal(data, &c); err != nil {
		return Creds{}, fmt.Errorf("couldn't parse Twitch chat credentials: %v", err)
	}
	return c, nil
}
