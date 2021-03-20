// Package db writes events to a database.
package db

import (
	"context"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/option"

	"github.com/aerionblue/pizzafest/donation"
)

type Client struct {
	client *firestore.Client
	now    func() time.Time
}

func NewClient(ctx context.Context, credsPath string) (*Client, error) {
	var options []option.ClientOption
	if credsPath != "" {
		options = append(options, option.WithCredentialsFile(credsPath))
	}
	client, err := firestore.NewClient(ctx, "pizza-fest", options...)
	if err != nil {
		return nil, err
	}
	return &Client{client: client, now: time.Now}, nil
}

func (c *Client) RecordDonation(ev donation.Event) error {
	donations := c.client.Collection("donations")
	doc := donationDoc{
		ISOTimestamp: c.now().UTC().Format(time.RFC3339Nano),
		Owner:        ev.Owner,
		Value:        ev.DollarValue(),
		SubCount:     ev.SubCount,
		SubTier:      ev.SubTier.Marshal(),
		SubMonths:    ev.SubMonths,
		Bits:         ev.Bits,
	}
	// TODO(aerion): Plumb through a context from the IRC bot.
	_, _, err := donations.Add(context.TODO(), doc)
	return err
}

// donationDoc is a Firestore document representing a donation.Event.
type donationDoc struct {
	ISOTimestamp string `firestore:"timestamp"`
	Owner        string `firestore:"owner"`
	Value        int    `firestore:"value"`
	SubCount     int    `firestore:"subCount,omitempty"`
	SubTier      int    `firestore:"subTier,omitempty"`
	SubMonths    int    `firestore:"subMonths,omitempty"`
	Bits         int    `firestore:"bits,omitempty"`
}
