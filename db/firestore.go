package db

import (
	"context"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/option"

	"github.com/aerionblue/pizzafest/donation"
)

type firestoreClient struct {
	client *firestore.Client
	now    func() time.Time
}

func NewFirestoreClient(ctx context.Context, credsPath string) (*firestoreClient, error) {
	var options []option.ClientOption
	if credsPath != "" {
		options = append(options, option.WithCredentialsFile(credsPath))
	}
	client, err := firestore.NewClient(ctx, "pizza-fest", options...)
	if err != nil {
		return nil, err
	}
	return &firestoreClient{client: client, now: time.Now}, nil
}

func (c *firestoreClient) RecordDonation(ev donation.Event) error {
	donations := c.client.Collection("donations")
	doc := donationDoc{
		ISOTimestamp: c.now().UTC().Format(time.RFC3339Nano),
		Owner:        ev.Owner,
		Value:        ev.DollarValue(),
		SubCount:     ev.SubCount,
		SubTier:      ev.SubTier.Marshal(),
		SubMonths:    ev.SubMonths,
		Cents:        ev.Cents,
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
	Cents        int    `firestore:"cents,omitempty"`
	Bits         int    `firestore:"bits,omitempty"`
	Message      string `firestore:"message,omitempty"`
}
