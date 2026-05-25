package natsclient

import "testing"

func TestErrorCodes_AreStableStrings(t *testing.T) {
	cases := map[string]string{
		"connect_failed":        CodeConnectFailed,
		"auth_ambiguous":        CodeAuthAmbiguous,
		"jetstream_unavailable": CodeJetStreamUnavailable,
		"stream_not_found":      CodeStreamNotFound,
		"stream_op_failed":      CodeStreamOpFailed,
		"stream_config_invalid": CodeStreamConfigInvalid,
		"consumer_op_failed":    CodeConsumerOpFailed,
		"publish_failed":        CodePublishFailed,
		"decode_failed":         CodeDecodeFailed,
	}
	for want, got := range cases {
		if got != want {
			t.Errorf("constant value drift: got %q, want %q", got, want)
		}
	}
}
