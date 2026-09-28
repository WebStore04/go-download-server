package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

var gmt9 = time.FixedZone("GMT+9", 9*60*60)

func formatGMT9(t time.Time) string {
	return t.In(gmt9).Format("2006-01-02 15:04:05")
}

func formatReadable(rec downloadRecord) string {
	return fmt.Sprintf("Time (GMT+9): %s\nCountry, city (IP): %s\nFile: %s\nOperating system: %s\nPC name: %s\n\n",
		oneLine(rec.Time), oneLine(formatLocation(rec.Location, rec.IP)), oneLine(rec.File), oneLine(rec.OS), oneLine(rec.PC))
}

func formatLocation(loc, ip string) string {
	loc = strings.TrimSpace(loc)
	ip = strings.TrimSpace(ip)
	switch {
	case loc == "" && ip == "":
		return "unknown"
	case loc == "":
		return ip
	case ip == "":
		return loc
	default:
		return loc + " (" + ip + ")"
	}
}

func clientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func osInfo(userAgent string) string {
	ua := strings.ToLower(userAgent)
	switch {
	case strings.Contains(ua, "android"):
		return "Android"
	case strings.Contains(ua, "iphone"), strings.Contains(ua, "ipad"):
		return "iOS"
	case strings.Contains(ua, "windows"), strings.Contains(ua, "win64"), strings.Contains(ua, "win32"):
		return "Windows"
	case strings.Contains(ua, "mac os"), strings.Contains(ua, "macintosh"), strings.Contains(ua, "darwin"):
		return "macOS"
	case strings.Contains(ua, "linux"), strings.Contains(ua, "x11"):
		return "Linux"
	default:
		return "unknown"
	}
}

var geoEndpoint = "http://ip-api.com/json/%s?fields=status,country,city"

var geoClient = &http.Client{Timeout: 2 * time.Second}

var (
	locationMu    sync.Mutex
	locationCache = map[string]string{}
)

func hostKind(ip string) (string, string) {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return "", "unknown"
	}
	canon := parsed.String()
	switch {
	case parsed.IsLoopback():
		return canon, "localhost"
	case parsed.IsPrivate() || parsed.IsLinkLocalUnicast() || parsed.IsUnspecified():
		return canon, "private"
	default:
		return canon, ""
	}
}

func lookupLocation(ip string) string {
	ip, kind := hostKind(ip)
	if kind != "" {
		return kind
	}
	locationMu.Lock()
	if loc, ok := locationCache[ip]; ok {
		locationMu.Unlock()
		return loc
	}
	locationMu.Unlock()

	loc := fetchLocation(ip)
	if loc != "unknown" {
		locationMu.Lock()
		locationCache[ip] = loc
		locationMu.Unlock()
	}
	return loc
}

func fetchLocation(ip string) string {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf(geoEndpoint, ip), nil)
	if err != nil {
		return "unknown"
	}
	resp, err := geoClient.Do(req)
	if err != nil {
		return "unknown"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "unknown"
	}
	var result struct {
		Status  string
		Country string
		City    string
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&result); err != nil {
		return "unknown"
	}
	if !strings.EqualFold(result.Status, "success") {
		return "unknown"
	}
	return joinPlace(result.Country, result.City)
}

func joinPlace(country, city string) string {
	country = strings.TrimSpace(country)
	city = strings.TrimSpace(city)
	if strings.EqualFold(country, city) {
		city = ""
	}
	switch {
	case country == "" && city == "":
		return "unknown"
	case country == "":
		return city
	case city == "":
		return country
	default:
		return country + ", " + city
	}
}

func lookupPCName(ip string) string {
	ip, kind := hostKind(ip)
	if kind == "unknown" || kind == "localhost" {
		return kind
	}
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		return "unknown"
	}
	return strings.TrimSuffix(names[0], ".")
}
