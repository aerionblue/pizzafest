package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
)

type BotConfig struct {
	Spreadsheet SpreadsheetConfig
}

type SpreadsheetConfig struct {
	ID        string
	SheetName string
}

func ParseBotConfig(path string) (BotConfig, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return BotConfig{}, fmt.Errorf("could not read bot config file: %v", err)
	}

	var cfg BotConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return BotConfig{}, fmt.Errorf("error parsing bot config file: %v", err)
	}
	return cfg, nil
}
