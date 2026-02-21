package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/fatih/color"
)

const cfBaseURL = "https://api.cloudflare.com/client/v4"

var (
	info = color.New(color.FgGreen).PrintlnFunc()
	errc = color.New(color.FgRed).PrintlnFunc()
)

type Config struct {
	PollInterval       int    `toml:"poll_interval"`
	CloudflareAPIToken string `toml:"cloudflare_api_token"`
	IPProvider         string `toml:"ip_provider"`
	Zones              []Zone `toml:"zone"`
}

type Zone struct {
	Name    string   `toml:"name"`
	Records []Record `toml:"records"`
}

type Record struct {
	Name    string `toml:"name"`
	Type    string `toml:"type"`
	Proxied bool   `toml:"proxied"`
	Target  string `toml:"target"`
}

type recordCache struct {
	zoneID   string
	recordID string
}

func logInfo(msg string) {
	fmt.Printf("[%s] ", time.Now().Format("2006-01-02 15:04:05"))
	info(msg)
}

func logError(msg string) {
	fmt.Printf("[%s] ", time.Now().Format("2006-01-02 15:04:05"))
	errc(msg)
}

func main() {
	cfgPath := "config.toml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	var cfg Config
	if _, err := toml.DecodeFile(cfgPath, &cfg); err != nil {
		logError(fmt.Sprintf("Failed to load config: %s", err))
		os.Exit(1)
	}

	if cfg.CloudflareAPIToken == "" {
		logError("cloudflare_api_token is required in config")
		os.Exit(1)
	}
	if cfg.IPProvider == "" {
		logError("ip_provider is required in config")
		os.Exit(1)
	}
	if len(cfg.Zones) == 0 {
		logError("At least one [[zone]] must be defined in config")
		os.Exit(1)
	}

	interval := 300
	if cfg.PollInterval > 0 {
		interval = cfg.PollInterval
	}

	for zi := range cfg.Zones {
		for ri := range cfg.Zones[zi].Records {
			if cfg.Zones[zi].Records[ri].Type == "" {
				cfg.Zones[zi].Records[ri].Type = "A"
			}
		}
	}

	cache := make(map[string]map[string]*recordCache)
	for _, z := range cfg.Zones {
		cache[z.Name] = make(map[string]*recordCache)
		for _, r := range z.Records {
			cache[z.Name][r.Name] = &recordCache{}
		}
	}

	lastIP := ""

	for {
		ip, err := getPublicIP(fmt.Sprintf("https://%s", cfg.IPProvider))
		if err != nil {
			logError(fmt.Sprintf("Failed to get public IP: %s", err))
			logInfo(fmt.Sprintf("Checking again in %s", formatDuration(interval)))
			time.Sleep(time.Duration(interval) * time.Second)
			continue
		}

		ipChanged := ip != lastIP
		if ipChanged {
			logInfo(fmt.Sprintf("Public IP changed to %s", ip))
			lastIP = ip
		} else {
			logInfo(fmt.Sprintf("IP hasn't changed"))
		}

		for _, zone := range cfg.Zones {
			zoneCache := cache[zone.Name]

			if zoneCache["__zone__"] == nil {
				zoneCache["__zone__"] = &recordCache{}
			}
			if zoneCache["__zone__"].zoneID == "" {
				zoneID, err := getZoneID(cfg.CloudflareAPIToken, zone.Name)
				if err != nil {
					logError(fmt.Sprintf("[%s] Failed to get zone ID: %s", zone.Name, err))
					continue
				}
				zoneCache["__zone__"].zoneID = zoneID
			}
			zoneID := zoneCache["__zone__"].zoneID

			for _, record := range zone.Records {
				isDynamic := record.Type == "A" || record.Type == "AAAA"

				if isDynamic && !ipChanged {
					continue
				}
				if !isDynamic && zoneCache[record.Name].recordID != "" {
					continue
				}

				rc := zoneCache[record.Name]

				if rc.recordID == "" {
					recordID, err := getOrCreateRecord(
						cfg.CloudflareAPIToken,
						zoneID,
						zone.Name,
						record,
						ip,
					)
					if err != nil {
						logError(fmt.Sprintf("[%s] [%s] Failed to get/create record: %s", zone.Name, record.Name, err))
						continue
					}
					rc.recordID = recordID
				}

				if !isDynamic {
					continue
				}

				if err := updateRecord(cfg.CloudflareAPIToken, zoneID, rc.recordID, zone.Name, record, ip); err != nil {
					logError(fmt.Sprintf("[%s] [%s] Failed to update record: %s", zone.Name, record.Name, err))
					rc.recordID = ""
					continue
				}

				logInfo(fmt.Sprintf("[%s] [%s %s] Updated to %s (proxied=%v)", zone.Name, record.Type, record.Name, ip, record.Proxied))
			}
		}

		logInfo(fmt.Sprintf("Checking again in %s", formatDuration(interval)))
		time.Sleep(time.Duration(interval) * time.Second)
	}
}

func formatDuration(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	seconds = seconds % 60
	if minutes < 60 {
		if seconds == 0 {
			return fmt.Sprintf("%dm", minutes)
		}
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	hours := minutes / 60
	minutes = minutes % 60
	if minutes == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh %dm", hours, minutes)
}

func getPublicIP(provider string) (string, error) {
	resp, err := http.Get(provider)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	return string(bytes.TrimSpace(buf[:n])), nil
}

func getZoneID(token, zoneName string) (string, error) {
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/zones?name=%s", cfBaseURL, zoneName), nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var data struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	if len(data.Result) == 0 {
		return "", fmt.Errorf("zone %q not found", zoneName)
	}
	return data.Result[0].ID, nil
}

func recordFQDN(recordName, zoneName string) string {
	if recordName == "@" {
		return zoneName
	}
	return recordName + "." + zoneName
}

func getOrCreateRecord(token, zoneID, zoneName string, record Record, ip string) (string, error) {
	fqdn := recordFQDN(record.Name, zoneName)

	req, _ := http.NewRequest(
		"GET",
		fmt.Sprintf("%s/zones/%s/dns_records?type=%s&name=%s", cfBaseURL, zoneID, record.Type, fqdn),
		nil,
	)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var list struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return "", err
	}
	if len(list.Result) > 0 {
		return list.Result[0].ID, nil
	}

	logInfo(fmt.Sprintf("Record %s %s not found, creating it...", record.Type, fqdn))

	body := buildRecordBody(record.Type, fqdn, resolveContent(record, zoneName, ip), record.Proxied)
	buf, _ := json.Marshal(body)

	req, _ = http.NewRequest("POST", fmt.Sprintf("%s/zones/%s/dns_records", cfBaseURL, zoneID), bytes.NewReader(buf))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("create record failed: %s", resp.Status)
	}

	var createResp struct {
		Result struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return "", err
	}
	return createResp.Result.ID, nil
}

func updateRecord(token, zoneID, recordID, zoneName string, record Record, ip string) error {
	fqdn := recordFQDN(record.Name, zoneName)
	body := buildRecordBody(record.Type, fqdn, resolveContent(record, zoneName, ip), record.Proxied)
	buf, _ := json.Marshal(body)

	req, _ := http.NewRequest(
		"PUT",
		fmt.Sprintf("%s/zones/%s/dns_records/%s", cfBaseURL, zoneID, recordID),
		bytes.NewReader(buf),
	)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("update failed: %s", resp.Status)
	}
	return nil
}

func resolveContent(record Record, zoneName, ip string) string {
	switch record.Type {
	case "A", "AAAA":
		return ip
	default:
		if record.Target != "" {
			return record.Target
		}
		return zoneName
	}
}

func buildRecordBody(recordType, name, content string, proxied bool) map[string]interface{} {
	ttl := 300
	if proxied {
		ttl = 1
	}
	return map[string]interface{}{
		"type":    recordType,
		"name":    name,
		"content": content,
		"ttl":     ttl,
		"proxied": proxied,
	}
}
