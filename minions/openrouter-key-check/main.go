package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

func main() {
	values, err := godotenv.Read(".env")
	if err != nil {
		fmt.Println("Cannot parse private .env")
		os.Exit(1)
	}
	key := strings.TrimSpace(values["OPENROUTER_API_KEY"])
	if key == "" || strings.ContainsAny(key, "\r\n") {
		fmt.Println("Key missing or invalid in .env")
		os.Exit(1)
	}
	client := &http.Client{Timeout: 25 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("GET", "https://openrouter.ai/api/v1/key", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	response, err := client.Do(req)
	if err != nil {
		fmt.Println("Authenticated request: transport unavailable")
		os.Exit(1)
	}
	defer response.Body.Close()
	fmt.Println("Authentication HTTP:", response.StatusCode)
	if response.StatusCode != 200 {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(&failure) == nil {
			message := strings.ReplaceAll(failure.Error.Message, key, "[redacted]")
			message = regexp.MustCompile(`sk-or-[A-Za-z0-9_-]+`).ReplaceAllString(message, "[redacted]")
			if len(message) > 300 {
				message = "provider error message omitted"
			}
			fmt.Println("Provider reason:", message)
		}
		fmt.Println("Expected key prefix:", strings.HasPrefix(key, "sk-or-v1-"))
		fmt.Println("Embedded whitespace:", strings.ContainsAny(key, " \t\r\n"))
		os.Exit(1)
	}
	var info struct {
		Data *struct {
			FreeDaily *struct {
				Used      int `json:"used"`
				Limit     int `json:"limit"`
				Remaining int `json:"remaining"`
			} `json:"free_model_daily_requests"`
			IsFreeTier      bool     `json:"is_free_tier"`
			IsManagementKey bool     `json:"is_management_key"`
			LimitRemaining  *float64 `json:"limit_remaining"`
			ExpiresAt       *string  `json:"expires_at"`
		}
	}
	if json.NewDecoder(io.LimitReader(response.Body, 512<<10)).Decode(&info) != nil || info.Data == nil {
		fmt.Println("Invalid key-info response")
		os.Exit(1)
	}
	fmt.Println("is_free_tier:", info.Data.IsFreeTier)
	if info.Data.FreeDaily != nil {
		fmt.Printf("Free daily quota: used=%d limit=%d remaining=%d\n", info.Data.FreeDaily.Used, info.Data.FreeDaily.Limit, info.Data.FreeDaily.Remaining)
	} else {
		fmt.Println("Free daily quota: not reported by API")
	}
	fmt.Println("is_management_key:", info.Data.IsManagementKey)
	if info.Data.LimitRemaining == nil {
		fmt.Println("Key spending limit: unset")
	} else {
		fmt.Println("Key remaining limit:", *info.Data.LimitRemaining)
	}
	if info.Data.ExpiresAt == nil {
		fmt.Println("Key expiry: unset")
	} else {
		fmt.Println("Key expiry:", *info.Data.ExpiresAt)
	}
	fmt.Println("Configured reserve enabled:", values["NEWS_LIBRARY_OPENROUTER_ENABLED"] == "true")
	models := values["NEWS_LIBRARY_OPENROUTER_MODELS"]
	if models == "" {
		models = "[]"
	}
	var configured []string
	if json.Unmarshal([]byte(models), &configured) == nil {
		fmt.Println("Configured reserve model count:", len(configured))
	} else {
		fmt.Println("Configured reserve model JSON: invalid")
	}
	if process := os.Getenv("OPENROUTER_API_KEY"); process != "" {
		fmt.Println("Process env overrides file key:", process != key)
	}
	r, err := client.Get("https://openrouter.ai/api/v1/models")
	if err != nil {
		fmt.Println("Model catalog: transport unavailable")
		return
	}
	defer r.Body.Close()
	fmt.Println("Model catalog HTTP:", r.StatusCode)
	if r.StatusCode != 200 {
		return
	}
	var catalog struct {
		Data []struct {
			ID      string
			Pricing map[string]json.RawMessage
		}
	}
	if json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&catalog) != nil {
		fmt.Println("Invalid catalog")
		return
	}
	zero := regexp.MustCompile(`^-?0+(\.0+)?([eE][+-]?[0-9]{1,4})?$`)
	free := []string{}
	for _, m := range catalog.Data {
		if !strings.HasSuffix(m.ID, ":free") || strings.HasPrefix(m.ID, "openrouter/") {
			continue
		}
		valid := len(m.Pricing["prompt"]) > 0 && len(m.Pricing["completion"]) > 0
		for _, p := range m.Pricing {
			var s string
			if json.Unmarshal(p, &s) != nil {
				s = string(p)
			}
			if len(s) > 128 || !zero.MatchString(s) {
				valid = false
			}
		}
		if valid {
			free = append(free, m.ID)
		}
	}
	fmt.Println("Catalog concrete zero-price free IDs:", len(free))
	for i, id := range free {
		if i >= 8 {
			break
		}
		fmt.Println("  " + id)
	}
	fmt.Println("Generation requests: 0; private key and full API responses not logged")
}
