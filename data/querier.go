// Code generated by sqlc. DO NOT EDIT.

package data

import (
	"context"
)

type Querier interface {
	CreateOrder(ctx context.Context, arg CreateOrderParams) (Order, error)
	GetOrder(ctx context.Context, id int32) (Order, error)
}

var _ Querier = (*Queries)(nil)
