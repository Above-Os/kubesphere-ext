package lldap

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-resty/resty/v2"
)

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token        string `json:"token"`        // 假设返回一个 token
	RefreshToken string `json:"refreshToken"` // 假设返回一个 message
}

func login(url, username, password string) (*LoginResponse, error) {
	creds := LoginRequest{
		Username: username,
		Password: password,
	}
	url = fmt.Sprintf("%s/auth/simple/login", url)
	client := resty.New()
	resp, err := client.SetTimeout(5*time.Second).R().
		SetHeader("Content-Type", "application/json").
		SetBody(creds).Post(url)
	if err != nil {
		return nil, err
	}
	var response LoginResponse
	err = json.Unmarshal(resp.Body(), &response)
	if err != nil {
		return nil, err
	}
	return &response, nil
}
