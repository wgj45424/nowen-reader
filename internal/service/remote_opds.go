package service

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const remoteOPDSLimit = 2 << 20

var remoteOPDSClient = &http.Client{
	Transport: &http.Transport{
		DialContext:            dialPublicOPDS,
		TLSHandshakeTimeout:    5 * time.Second,
		IdleConnTimeout:        30 * time.Second,
		MaxIdleConns:           16,
		MaxResponseHeaderBytes: 64 << 10,
	},
	CheckRedirect: checkOPDSRedirect,
}

func checkOPDSRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 || !validOPDSURL(req.URL) {
		return errors.New("invalid OPDS redirect")
	}
	first := via[0]
	if first.Header.Get("Authorization") != "" &&
		(req.URL.Scheme != first.URL.Scheme || !strings.EqualFold(req.URL.Host, first.URL.Host)) {
		return errors.New("authenticated OPDS redirect changed origin")
	}
	req.Header.Del("Referer")
	return nil
}

var nonPublicOPDSNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
}

var globalIPv6Unicast = netip.MustParsePrefix("2000::/3")

func isPublicOPDSIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || (ip.Is6() && !globalIPv6Unicast.Contains(ip)) {
		return false
	}
	for _, network := range nonPublicOPDSNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

func dialPublicOPDS(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 4 * time.Second}
	for _, ip := range ips {
		if isPublicOPDSIP(ip) {
			// Dial the exact address just checked, not the hostname again.
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
		}
	}
	return nil, errors.New("could not connect to a public OPDS address")
}

func validOPDSURL(u *url.URL) bool {
	return (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" &&
		u.User == nil && !strings.Contains(u.Host, "%") && len(u.String()) <= 4096
}

type RemoteOPDSCatalog struct {
	Title   string            `json:"title"`
	Next    string            `json:"next,omitempty"`
	Entries []remoteOPDSEntry `json:"entries"`
}

type remoteOPDSEntry struct {
	Title string           `json:"title"`
	Links []remoteOPDSLink `json:"links"`
}

type remoteOPDSLink struct {
	URL   string `json:"url"`
	Kind  string `json:"kind"`
	Title string `json:"title,omitempty"`
}

func requestRemoteOPDS(ctx context.Context, address, username, password, accept string) (*http.Response, error) {
	u, err := url.Parse(address)
	if err != nil || !validOPDSURL(u) {
		return nil, errors.New("enter a valid HTTP(S) catalog URL without embedded credentials")
	}
	if (username != "" || password != "") && u.Scheme != "https" {
		return nil, errors.New("credentials are only allowed for HTTPS catalogs")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("invalid catalog URL")
	}
	req.Header.Set("Accept", accept)
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}
	resp, err := remoteOPDSClient.Do(req)
	if err != nil {
		return nil, errors.New("could not connect to the public OPDS server")
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("OPDS server returned HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// FetchRemoteOPDS fetches an OPDS 1.x feed without forwarding local credentials.
func FetchRemoteOPDS(ctx context.Context, address, username, password string) (*RemoteOPDSCatalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := requestRemoteOPDS(ctx, address, username, password, "application/atom+xml, application/xml;q=0.9, text/xml;q=0.8")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, remoteOPDSLimit+1))
	if err != nil {
		return nil, errors.New("could not read the catalog response")
	}
	if len(body) > remoteOPDSLimit {
		return nil, errors.New("catalog response exceeds 2 MiB")
	}
	return parseRemoteOPDS(body, resp.Request.URL)
}

type atomOPDSLink struct {
	Base  string `xml:"http://www.w3.org/XML/1998/namespace base,attr"`
	Href  string `xml:"href,attr"`
	Rel   string `xml:"rel,attr"`
	Type  string `xml:"type,attr"`
	Title string `xml:"title,attr"`
}

func resolveOPDSURL(base *url.URL, raw string) *url.URL {
	if base == nil || raw == "" || strings.Contains(raw, `\`) {
		return nil
	}
	reference, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	resolved := base.ResolveReference(reference)
	if !validOPDSURL(resolved) {
		return nil
	}
	return resolved
}

func atomOPDSBase(base *url.URL, raw string) *url.URL {
	if raw == "" {
		return base
	}
	return resolveOPDSURL(base, raw)
}

func parseRemoteOPDS(body []byte, base *url.URL) (*RemoteOPDSCatalog, error) {
	var feed struct {
		XMLName xml.Name       `xml:"http://www.w3.org/2005/Atom feed"`
		Base    string         `xml:"http://www.w3.org/XML/1998/namespace base,attr"`
		Title   string         `xml:"http://www.w3.org/2005/Atom title"`
		Links   []atomOPDSLink `xml:"http://www.w3.org/2005/Atom link"`
		Entries []struct {
			Base  string         `xml:"http://www.w3.org/XML/1998/namespace base,attr"`
			Title string         `xml:"http://www.w3.org/2005/Atom title"`
			Links []atomOPDSLink `xml:"http://www.w3.org/2005/Atom link"`
		} `xml:"http://www.w3.org/2005/Atom entry"`
	}
	if err := xml.NewDecoder(bytes.NewReader(body)).Decode(&feed); err != nil {
		return nil, errors.New("the response is not a valid OPDS 1.x Atom feed")
	}
	base = atomOPDSBase(base, feed.Base)
	catalog := &RemoteOPDSCatalog{Title: feed.Title, Entries: make([]remoteOPDSEntry, 0, len(feed.Entries))}
	for _, link := range feed.Links {
		if link.Rel == "next" {
			if next := resolveOPDSURL(atomOPDSBase(base, link.Base), link.Href); next != nil {
				catalog.Next = next.String()
				break
			}
		}
	}
	remaining := 2000
	for _, entry := range feed.Entries {
		entryBase := atomOPDSBase(base, entry.Base)
		item := remoteOPDSEntry{Title: entry.Title, Links: []remoteOPDSLink{}}
		for _, link := range entry.Links {
			mediaType, _, _ := mime.ParseMediaType(link.Type)
			var kind string
			switch {
			case link.Rel == "http://opds-spec.org/acquisition" || strings.HasPrefix(link.Rel, "http://opds-spec.org/acquisition/"):
				kind = "acquisition"
			case mediaType == "application/atom+xml" && (link.Rel == "" || link.Rel == "alternate" || link.Rel == "subsection"):
				kind = "catalog"
			default:
				continue
			}
			if target := resolveOPDSURL(atomOPDSBase(entryBase, link.Base), link.Href); target != nil {
				remaining--
				if remaining < 0 {
					return nil, errors.New("catalog page has too many links")
				}
				item.Links = append(item.Links, remoteOPDSLink{URL: target.String(), Kind: kind, Title: link.Title})
			}
		}
		catalog.Entries = append(catalog.Entries, item)
	}
	return catalog, nil
}
