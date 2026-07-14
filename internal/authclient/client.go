// Package authclient — gRPC-клиент к Auth service (ValidateToken).
// Реализует httpapi.TokenValidator.
package authclient

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	authv1 "github.com/1RAFTIK1/linkpulse-contracts/gen/go/auth/v1"
)

// callTimeout — потолок на один вызов ValidateToken: авторизация стоит
// на пути запроса пользователя, ждать дольше нельзя.
const callTimeout = 3 * time.Second

type Client struct {
	conn *grpc.ClientConn
	api  authv1.AuthServiceClient
}

func New(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("auth grpc client: %w", err)
	}
	return &Client{conn: conn, api: authv1.NewAuthServiceClient(conn)}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

// Validate проверяет токен через Auth service.
func (c *Client) Validate(ctx context.Context, token string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	resp, err := c.api.ValidateToken(ctx, &authv1.ValidateTokenRequest{Token: token})
	if err != nil {
		return "", false, fmt.Errorf("validate token rpc: %w", err)
	}
	if !resp.GetValid() {
		return "", false, nil
	}
	return resp.GetUserId(), true, nil
}
