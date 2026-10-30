package main

import (
	"bytes"
	"encoding/binary"
	"mime/multipart"
	"net/http/httptest"
	"os/exec"
	"testing"
)

func testWAV() []byte {
	const size = 8000
	b := make([]byte, 44+size)
	copy(b[0:4], "RIFF")
	binary.LittleEndian.PutUint32(b[4:8], 36+size)
	copy(b[8:16], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:20], 16)
	binary.LittleEndian.PutUint16(b[20:22], 1)
	binary.LittleEndian.PutUint16(b[22:24], 1)
	binary.LittleEndian.PutUint32(b[24:28], 8000)
	binary.LittleEndian.PutUint32(b[28:32], 16000)
	binary.LittleEndian.PutUint16(b[32:34], 2)
	binary.LittleEndian.PutUint16(b[34:36], 16)
	copy(b[36:40], "data")
	binary.LittleEndian.PutUint32(b[40:44], size)
	return b
}
func TestRealFFProbeUploadTempWithoutExtension(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("requires ffprobe")
	}
	srv := NewServer(t.TempDir(), probeFF)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	f, err := writer.CreateFormFile("files", "hello.wav")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(testWAV()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/admin/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Soundboard-Nickname", "Tester")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("real ffprobe import: %d %s", rec.Code, rec.Body.String())
	}
	sound, ok := srv.lib.Get("hello.wav")
	if !ok || sound.DurationMS != 500 || sound.UploadedBy != "Tester" {
		t.Fatalf("imported sound: %+v", sound)
	}
}
