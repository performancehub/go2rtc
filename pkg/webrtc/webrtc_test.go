package webrtc

import (
	"strings"
	"testing"

	"github.com/pion/webrtc/v3"
	"github.com/stretchr/testify/require"
)

func TestAlexa(t *testing.T) {
	// from https://github.com/AlexxIT/go2rtc/issues/825
	offer := `v=0
o=- 3911343731 3911343731 IN IP4 0.0.0.0
s=a 2 z
c=IN IP4 0.0.0.0
t=0 0
a=group:BUNDLE audio0 video0
m=audio 1 UDP/TLS/RTP/SAVPF 96 0 8
a=candidate:1 1 UDP 2013266431 52.90.193.210 60128 typ host
a=candidate:2 1 TCP 1015021823 52.90.193.210 9 typ host tcptype active
a=candidate:3 1 TCP 1010827519 52.90.193.210 45962 typ host tcptype passive
a=candidate:1 2 UDP 2013266430 52.90.193.210 46109 typ host
a=candidate:2 2 TCP 1015021822 52.90.193.210 9 typ host tcptype active
a=candidate:3 2 TCP 1010827518 52.90.193.210 53795 typ host tcptype passive
a=setup:actpass
a=extmap:3 http://www.webrtc.org/experiments/rtp-hdrext/abs-send-time
a=rtpmap:96 opus/48000/2
a=rtpmap:0 PCMU/8000
a=rtpmap:8 PCMA/8000
a=rtcp:9 IN IP4 0.0.0.0
a=rtcp-mux
a=sendrecv
a=mid:audio0
a=ssrc:3573704076 cname:user3856789923@host-9dd1dd33
a=ice-ufrag:gxfV
a=ice-pwd:KepKrlQ1+LD+RGTAFaqVck
a=fingerprint:sha-256 A2:93:53:50:E4:2F:C5:4E:DF:7C:70:99:5A:A7:39:50:1A:63:E5:B2:CA:73:70:7A:C5:F4:01:BF:BD:99:57:FC
m=video 1 UDP/TLS/RTP/SAVPF 99
a=candidate:1 1 UDP 2013266431 52.90.193.210 60128 typ host
a=candidate:1 2 UDP 2013266430 52.90.193.210 46109 typ host
a=candidate:2 1 TCP 1015021823 52.90.193.210 9 typ host tcptype active
a=candidate:3 1 TCP 1010827519 52.90.193.210 45962 typ host tcptype passive
a=candidate:3 2 TCP 1010827518 52.90.193.210 53795 typ host tcptype passive
a=candidate:2 2 TCP 1015021822 52.90.193.210 9 typ host tcptype active
b=AS:2500
a=setup:actpass
a=extmap:3 http://www.webrtc.org/experiments/rtp-hdrext/abs-send-time
a=rtpmap:99 H264/90000
a=rtcp:9 IN IP4 0.0.0.0
a=rtcp-mux
a=sendrecv
a=mid:video0
a=rtcp-fb:99 nack
a=rtcp-fb:99 nack pli
a=rtcp-fb:99 ccm fir
a=ssrc:3778078295 cname:user3856789923@host-9dd1dd33
a=ice-ufrag:gxfV
a=ice-pwd:KepKrlQ1+LD+RGTAFaqVck
a=fingerprint:sha-256 A2:93:53:50:E4:2F:C5:4E:DF:7C:70:99:5A:A7:39:50:1A:63:E5:B2:CA:73:70:7A:C5:F4:01:BF:BD:99:57:FC
`

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	require.Nil(t, err)

	conn := NewConn(pc)
	err = conn.SetOffer(offer)
	require.Nil(t, err)

	_, err = conn.GetAnswer()
	require.Nil(t, err)
}

func TestSanitizeSDP(t *testing.T) {
	// SDP with duplicate msid attributes (both legacy SSRC-level and modern media-level)
	// Chrome rejects this with "Duplicate a=msid lines detected"
	input := "v=0\r\n" +
		"o=- 1234567890 1234567890 IN IP4 127.0.0.1\r\n" +
		"s=-\r\n" +
		"t=0 0\r\n" +
		"m=video 9 UDP/TLS/RTP/SAVPF 97\r\n" +
		"a=rtpmap:97 H264/90000\r\n" +
		"a=ssrc:2815449682 cname:go2rtc\r\n" +
		"a=ssrc:2815449682 msid:go2rtc video\r\n" +
		"a=ssrc:2815449682 mslabel:go2rtc\r\n" +
		"a=ssrc:2815449682 label:video\r\n" +
		"a=msid:go2rtc video\r\n" +
		"a=sendonly\r\n"

	result := SanitizeSDP(input)

	// Should keep the modern a=msid: line
	if !strings.Contains(result, "a=msid:go2rtc video") {
		t.Errorf("Expected modern a=msid: line to be preserved")
	}

	// Should keep the cname line (not msid/mslabel/label)
	if !strings.Contains(result, "a=ssrc:2815449682 cname:go2rtc") {
		t.Errorf("Expected a=ssrc:XXX cname: line to be preserved")
	}

	// Should remove legacy SSRC-level msid
	if strings.Contains(result, "a=ssrc:2815449682 msid:") {
		t.Errorf("Expected legacy a=ssrc:XXX msid: line to be removed")
	}

	// Should remove legacy SSRC-level mslabel
	if strings.Contains(result, "a=ssrc:2815449682 mslabel:") {
		t.Errorf("Expected legacy a=ssrc:XXX mslabel: line to be removed")
	}

	// Should remove legacy SSRC-level label
	if strings.Contains(result, "a=ssrc:2815449682 label:") {
		t.Errorf("Expected legacy a=ssrc:XXX label: line to be removed")
	}

	// Should keep other lines
	if !strings.Contains(result, "a=rtpmap:97 H264/90000") {
		t.Errorf("Expected a=rtpmap: line to be preserved")
	}
	if !strings.Contains(result, "a=sendonly") {
		t.Errorf("Expected a=sendonly line to be preserved")
	}
}
