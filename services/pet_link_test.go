package services

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
)

func TestPetLinkRequestAndResponseValidation(t *testing.T) {
	request, err := NewPetLinkRequest("pet.state", map[string]any{"petId": "default"})
	if err != nil {
		t.Fatalf("NewPetLinkRequest() error = %v", err)
	}
	encoded, err := MarshalPetLinkRequest(request)
	if err != nil {
		t.Fatalf("MarshalPetLinkRequest() error = %v", err)
	}
	decoded, err := UnmarshalPetLinkRequest(encoded)
	if err != nil {
		t.Fatalf("UnmarshalPetLinkRequest() error = %v", err)
	}
	if decoded.Protocol != PetLinkProtocol || decoded.Version != PetLinkProtocolVersion || decoded.ID != request.ID || decoded.Method != request.Method {
		t.Fatalf("decoded request = %#v, want %#v", decoded, request)
	}

	badVersion := request
	badVersion.Version++
	if _, err := MarshalPetLinkRequest(badVersion); err == nil {
		t.Fatal("MarshalPetLinkRequest() accepted unsupported version")
	}

	response := PetLinkResponse{
		Protocol: PetLinkProtocol,
		Version:  PetLinkProtocolVersion,
		ID:       request.ID,
		OK:       true,
		Result:   json.RawMessage(`{"ok":true}`),
	}
	responseBytes, err := MarshalPetLinkResponse(response)
	if err != nil {
		t.Fatalf("MarshalPetLinkResponse() error = %v", err)
	}
	decodedResponse, err := UnmarshalPetLinkResponse(responseBytes)
	if err != nil {
		t.Fatalf("UnmarshalPetLinkResponse() error = %v", err)
	}
	if !decodedResponse.OK || string(decodedResponse.Result) != `{"ok":true}` {
		t.Fatalf("decoded response = %#v", decodedResponse)
	}

	badResponse := PetLinkResponse{
		Protocol: PetLinkProtocol,
		Version:  PetLinkProtocolVersion,
		ID:       request.ID,
		OK:       false,
	}
	if _, err := MarshalPetLinkResponse(badResponse); err == nil {
		t.Fatal("MarshalPetLinkResponse() accepted failed response without error")
	}
}

func TestPetLinkClientSerializesCallsAndReusesReader(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	serverDone := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(serverConn)
		for index := 0; index < 2; index++ {
			frame, err := readPetLinkFrame(reader)
			if err != nil {
				serverDone <- err
				return
			}
			request, err := UnmarshalPetLinkRequest(frame)
			if err != nil {
				serverDone <- err
				return
			}
			result, _ := json.Marshal(map[string]any{"method": request.Method, "index": index})
			response, err := MarshalPetLinkResponse(PetLinkResponse{
				Protocol: PetLinkProtocol,
				Version:  PetLinkProtocolVersion,
				ID:       request.ID,
				OK:       true,
				Result:   result,
			})
			if err != nil {
				serverDone <- err
				return
			}
			if err := writePetLinkFrame(serverConn, append(response, '\n')); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()

	client := newPetLinkClient(clientConn)
	var first, second map[string]any
	if err := client.Call(context.Background(), "first", nil, &first); err != nil {
		t.Fatalf("first Call() error = %v", err)
	}
	if err := client.Call(context.Background(), "second", nil, &second); err != nil {
		t.Fatalf("second Call() error = %v", err)
	}
	if first["method"] != "first" || second["method"] != "second" {
		t.Fatalf("results = %#v, %#v", first, second)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server loop error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server loop did not complete")
	}
}

func TestPetLinkClientReturnsRemoteError(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go func() {
		reader := bufio.NewReader(serverConn)
		frame, err := readPetLinkFrame(reader)
		if err != nil {
			return
		}
		request, err := UnmarshalPetLinkRequest(frame)
		if err != nil {
			return
		}
		response, _ := MarshalPetLinkResponse(PetLinkResponse{
			Protocol: PetLinkProtocol,
			Version:  PetLinkProtocolVersion,
			ID:       request.ID,
			Error:    &PetLinkRPCError{Code: "denied", Message: "not allowed"},
		})
		_ = writePetLinkFrame(serverConn, append(response, '\n'))
	}()

	err := newPetLinkClient(clientConn).Call(context.Background(), "pet.secret", nil, nil)
	var rpcErr *PetLinkRPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != "denied" {
		t.Fatalf("Call() error = %v, want denied RPC error", err)
	}
}
