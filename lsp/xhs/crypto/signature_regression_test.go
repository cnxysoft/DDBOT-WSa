package crypto

import (
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestBuildContentStringGETEncodesAndPreservesIterationOrder(t *testing.T) {
	cp := NewCryptoProcessor(DefaultCryptoConfig())

	params := map[string]string{
		"source":          "web_live",
		"room_id":         "570272590323152028",
		"request_user_id": "6a03ef620000000002000000",
		"client_type":     "1 2",
	}

	got := cp.buildContentString("GET", "/api/sns/red/live/web/v1/room/current_room_info", params, nil)
	want := "/api/sns/red/live/web/v1/room/current_room_info?room_id=570272590323152028&request_user_id=6a03ef620000000002000000&source=web_live&client_type=1+2"
	if got != want {
		t.Fatalf("buildContentString() = %q, want %q", got, want)
	}
}

func TestBuildContentStringGETWithoutParamsReturnsURI(t *testing.T) {
	cp := NewCryptoProcessor(DefaultCryptoConfig())

	got := cp.buildContentString("GET", "/api/sns/red/live/web/v1/room/current_room_info", nil, nil)
	want := "/api/sns/red/live/web/v1/room/current_room_info"
	if got != want {
		t.Fatalf("buildContentString() = %q, want %q", got, want)
	}
}

func TestSignCommonDataMarshalJSONOrder(t *testing.T) {
	data := SignCommonData{
		S0:  5,
		S1:  "",
		X0:  "1",
		X1:  "4.3.3",
		X2:  "Windows",
		X3:  "xhs-pc-web",
		X4:  "4.86.0",
		X5:  "a1",
		X6:  "",
		X7:  "",
		X8:  "b1",
		X9:  123,
		X10: 0,
		X11: "normal",
	}

	got, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	want := `{"s0":5,"s1":"","x0":"1","x1":"4.3.3","x2":"Windows","x3":"xhs-pc-web","x4":"4.86.0","x5":"a1","x6":"","x7":"","x8":"b1","x9":123,"x10":0,"x11":"normal"}`
	if string(got) != want {
		t.Fatalf("json.Marshal() = %s, want %s", got, want)
	}
}

func TestB1FingerprintMarshalJSONOrder(t *testing.T) {
	cp := NewCryptoProcessor(DefaultCryptoConfig())
	fp := cp.newB1Fingerprint(7, 1778642786483)

	got, err := json.Marshal(fp)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	want := `{"x33":"0","x34":"0","x35":"0","x36":"7","x37":"0|0|0|0|0|0|0|0|0|1|0|0|0|0|0|0|0|0|1|0|0|0|0|0","x38":"0|0|1|0|1|0|0|0|0|0|1|0|1|0|1|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0|0","x39":0,"x42":"3.4.4","x43":"742cc32c","x44":"1778642786483","x45":"__SEC_CAV__1-1-1-1-1|__SEC_WSA__|","x46":"false","x48":"","x49":"{list:[],type:}","x50":"","x51":"","x52":"","x82":"_0x17a2|_0x1954"}`
	if string(got) != want {
		t.Fatalf("json.Marshal() = %s, want %s", got, want)
	}
}

func TestGenerateB1MatchesPythonReferenceForFixedSubsetFingerprint(t *testing.T) {
	cp := NewCryptoProcessor(DefaultCryptoConfig())
	fp := cp.newB1Fingerprint(3, 1779135548869)

	fpJSON, err := json.Marshal(fp)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	ciphertext := cp.rc4Encrypt([]byte(cp.config.B1SecretKey), fpJSON)
	urlEncoded := urlEncodeBytes(ciphertext)
	parsed := parseURLEncoded(urlEncoded)
	got := cp.b64Encoder.Encode(parsed)

	want := "I38rHdgsjopgIvesdVwgIC+oIELmBZ5e3VwXLgFTIxS3bqwErFeexd0ekncAzMFYnqthIhJeSBMDKutRI3KsYorWHPtGrbV0P9WfIi/eWc6eYqtyQApPI37ekmR6QL+5Ii6sdnoeSfqYHqwl2qt5B0DoIx+PGDi/sVtkIxdsxuwr4qtiIhuaIE3e3LV0I3VTIC7e0utl2ADmsLveDSKsSPw5IEvsiVtJOqw8BuwfPpdeTFWOIx4TIiu6ZPwrPut5IvlaLbgs3qtxIxes1VwHIkumIkIyejgsY/WTge7eSqte/D7sDcpipedeYrDtIC6eDVw2IENsSqtlnlSuNjVtIvoekqt3cZ7sVo4gIESyIEle+AFDI3EPKI8BIiWIZPwAIvGj4sesYINsxVwSIC7ef96e0fhPIive6WrS8qwUIE7s1f0s6WAeiVtwpjJejPw5Ivlpz07eSuwALnAsWVw8IxI2I38isqwZgVtI4LTjoAve6peeYqwxIvAeS0Os1DZiIi7sjbos3amyIv6sdqwaICmygVtxgVw4IE7sVVtFIiAsiqtSIENsdutSHuwPnVtdIxkhIvVr27lk2Ive1utCIEDtIkJeYut4bYRtn/0ejgI7Ih4s2uwfJPwSI35skqwWGD5s6WAs3phwIhos3fOs3utscPwaICJsWPw5IiJekeqLICKejd/sfPtUIx7sxuwD4BYaIhQgIv5s1M6e6gvsiLdedVtsIkYTI3ilJutpIxElIEvsxbr38W=="
	if got != want {
		t.Fatalf("generate b1 = %q, want %q", got, want)
	}
}

func TestBuildPayloadArrayWithFixedInputsMatchesPythonReference(t *testing.T) {
	cp := NewCryptoProcessor(DefaultCryptoConfig())
	contentString := "/api/sns/red/live/web/v1/room/current_room_info?room_id=570272590323152028&request_user_id=6a03ef620000000002000000&source=web_live&client_type=1"
	payload := cp.buildPayloadArrayWithInputs(
		"f8b3d86fb356d7d2dfb5f4a5dca8c88d",
		"f8b3d86fb356d7d2dfb5f4a5dca8c88d",
		"19e1f5f14ed7uvezf7mssckdx6cy9ibrxn3l67lyh50000151403",
		"xhs-pc-web",
		contentString,
		1778642786.483,
		nil,
		0x12345678,
		17,
		23,
		1111,
	)

	gotPayloadHex := hex.EncodeToString(payload)
	wantPayloadHex := "7968602978563412b3185f1f9e0100004bd65e1f9e01000017000000570400009100000080cba017cb2eafaa343139653166356631346564377576657a66376d7373636b647836637939696272786e336c36376c796835303030303135313430330a7868732d70632d776562010bf9416767c9b583635e0744fa841502613310cd947fb354468729adf6f68a0b7517eb"
	if gotPayloadHex != wantPayloadHex {
		t.Fatalf("payload hex = %s, want %s", gotPayloadHex, wantPayloadHex)
	}

	xorHex := hex.EncodeToString(cp.bitOps.XorTransformArray(payload))
	wantXorHex := "08cb620c0fc5130f6e3f64d17de5b98dd6af6bfe4432f576492ea8afe1d877a58b499d2336b78071cb0b29a68b22ed313cf47c19695c09337925f9239bf3542980e25ad42bd714c097c849d5e0207a6843dd66ffcf2fb359a622a8d6aedcfe545d101152e14c24c68ad6d87ef1d5223548f6b3c1f97d668c67b5a662530c8401ada13c2464bce6c02fdcf840708a6533"
	if xorHex != wantXorHex {
		t.Fatalf("xor hex = %s, want %s", xorHex, wantXorHex)
	}

	gotX3 := cp.b64Encoder.EncodeX3(cp.bitOps.XorTransformArray(payload)[:cp.config.PayloadLength])
	wantX3 := "gRaKqMGsr96puVmCED0id2J8JGirR85V77jxY3cH2j0Q7I4dSYDMooZQPJJQyp4+uuClb0NogmSizEedOGSnPHqK0avYU+mMNlTzUDMFDOTqL0BGA/3A0JHKXSJpLuin1CMCnpsRzRJPUaT3l2nKSndVZlciE0JRIk0OHNRRTMbaxm9eIQAO9gGo3rf9KOnA"
	if gotX3 != wantX3 {
		t.Fatalf("x3 = %s, want %s", gotX3, wantX3)
	}
}
