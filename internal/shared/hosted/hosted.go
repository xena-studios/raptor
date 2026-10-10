// Package hosted is what Wings and the Panel agree on about Raptor Backup
// Storage (docs/PANEL.md#raptor-backup-storage): one Backblaze B2 bucket,
// reached through its S3 API, with a folder per node.
package hosted

import (
	"regexp"
	"strings"
)

// Bucket is the bucket's name. B2 bucket names are unique across all of
// Backblaze, so pinning it here means a "Raptor storage" destination can
// only ever send backups to Raptor's own bucket, whatever a Panel says.
const Bucket = "raptor-backup-storage"

// endpoint is B2's S3 endpoint, in any region.
var endpoint = regexp.MustCompile(`^s3\.[a-z0-9-]+\.backblazeb2\.com$`)

// Endpoint reports whether e is a B2 S3 endpoint.
func Endpoint(e string) bool {
	return endpoint.MatchString(e)
}

// Prefix is a node's folder in the bucket. Its B2 key reaches nothing else.
func Prefix(orgID, nodeID string) string {
	return "orgs/" + orgID + "/nodes/" + nodeID + "/"
}

// OrgPrefix is an org's folder: its nodes' folders.
func OrgPrefix(orgID string) string {
	return "orgs/" + orgID + "/"
}

// NodeOf returns the node a prefix is for, if it's a node's folder.
func NodeOf(prefix string) (string, bool) {
	rest, ok := strings.CutPrefix(prefix, "orgs/")
	if !ok {
		return "", false
	}
	org, rest, ok := strings.Cut(rest, "/nodes/")
	node, ok2 := strings.CutSuffix(rest, "/")
	if !ok || !ok2 || org == "" || node == "" || strings.Contains(org, "/") || strings.Contains(node, "/") {
		return "", false
	}
	return node, true
}

// Pricing (docs/DECISIONS.md #230).
const (
	// IncludedBytesPerNode is what each paid node includes.
	IncludedBytesPerNode = 10 << 30
	// CentsPerTB is the price past that, per TB-month (1e12 bytes).
	CentsPerTB = 1200
	// SoftCapBytes is the most an org stores during beta before it's asked
	// to get in touch.
	SoftCapBytes = 500 << 30
)
