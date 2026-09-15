package agentbin

import "testing"

// The digest is what keeps a rebuilt agent out of the old build's cache
// entry. If it stops varying with the embedded bytes, DSKY hands back stale
// media after a fix and nothing says so -- which cost a full Windows install
// to notice once already.
func TestDigestIdentifiesTheEmbeddedAgent(t *testing.T) {
	amd, arm := Digest(AMD64), Digest(ARM64)
	if amd == "" || arm == "" {
		t.Fatal("no digest at all")
	}
	if Available(AMD64) {
		if amd == "none" {
			t.Error("an agent is embedded for amd64 but the digest says none")
		}
		if len(amd) != 64 {
			t.Errorf("digest is %q", amd)
		}
		if amd == arm && Available(ARM64) {
			t.Error("both architectures have the same digest; the cache could not tell them apart")
		}
	} else if amd != "none" {
		t.Errorf("no agent is embedded for amd64 but the digest is %q", amd)
	}
	if got := Digest(AMD64); got != amd {
		t.Error("the same agent gave two different digests")
	}
}
