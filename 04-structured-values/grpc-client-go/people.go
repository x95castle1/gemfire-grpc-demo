package main

import (
	"context"
	"errors"
	"fmt"

	gemfire "grpc-client-go/gen/gemfirev1"
	personv1 "grpc-client-go/gen/personv1"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// The data set: people with deterministic fields, so the demo knows what every query should
// return. person(i) is always the same Person.
var (
	firstNames = []string{"Ada", "Grace", "Barbara", "Alan", "Edsger", "Margaret", "Linus", "Katherine", "Dennis", "Frances"}
	lastNames  = []string{"Lovelace", "Hopper", "Liskov", "Turing", "Dijkstra", "Hamilton", "Torvalds", "Johnson", "Ritchie", "Allen"}
	cities     = []struct{ city, country string }{
		{"London", "UK"}, {"Paris", "France"}, {"Berlin", "Germany"}, {"Madrid", "Spain"}, {"Rome", "Italy"},
		{"Boston", "USA"}, {"Austin", "USA"}, {"Toronto", "Canada"}, {"Tokyo", "Japan"}, {"Sydney", "Australia"},
	}
	nicknames = []string{"Ace", "Bear", "Chip", "Dot", "Echo"}
)

func personID(i int) string { return fmt.Sprintf("p-%05d", i) }

// person(i): age 18..90, role and active vary with i, 0..2 nicknames, and every 10th person
// (i % 10 == 9) has no address at all.
func person(i int) *personv1.Person {
	p := &personv1.Person{
		Id:        personID(i),
		FirstName: firstNames[(i/7)%len(firstNames)],
		LastName:  lastNames[(i/3)%len(lastNames)],
		Age:       int32(18 + (i*7)%73),
		Role:      personv1.Role(1 + i%3),
		Active:    i%4 != 0,
	}
	for n := range i % 3 {
		p.Nicknames = append(p.Nicknames, nicknames[(i+n)%len(nicknames)])
	}
	if i%10 != 9 {
		c := cities[(i/2)%len(cities)]
		p.Address = &personv1.Address{City: c.city, Country: c.country, Lines: []string{fmt.Sprintf("%d Main Street", i%200+1)}}
	}
	return p
}

// packed wraps a message in google.protobuf.Any, the way every structured value travels.
// The type URL is type.googleapis.com/<full message name>.
func packed(m proto.Message) *gemfire.Value {
	a, err := anypb.New(m)
	if err != nil {
		panic(err) // only fails for nil messages
	}
	return &gemfire.Value{ValueValue: &gemfire.Value_Any{Any: a}}
}

// People reads and writes Person messages over the plain Get/Put/PutStream RPCs.
type People struct {
	gemfire gemfire.GemFireClient
}

func newPeople(client gemfire.GemFireClient) *People { return &People{gemfire: client} }

// put stores person(i) under its id.
func (p *People) put(ctx context.Context, region string, i int) error {
	_, err := p.gemfire.Put(ctx, &gemfire.PutRequest{RegionName: region, Key: key(personID(i)), Value: packed(person(i))})
	return err
}

// get returns the Person stored under personID(i) (nil when absent) and its size in bytes.
func (p *People) get(ctx context.Context, region string, i int) (*personv1.Person, int, error) {
	r, err := p.gemfire.Get(ctx, &gemfire.GetRequest{RegionName: region, Key: key(personID(i))})
	if err != nil || r.GetValue().GetAny() == nil {
		return nil, 0, err
	}
	out := &personv1.Person{}
	if err := r.GetValue().GetAny().UnmarshalTo(out); err != nil {
		return nil, 0, err // a different type is stored under this key
	}
	return out, proto.Size(r.GetValue().GetAny()), nil
}

// load writes person(0..count-1) with PutStream, 500 per message.
func (p *People) load(ctx context.Context, region string, count int) error {
	stream, err := p.gemfire.PutStream(ctx)
	if err != nil {
		return err
	}
	msg := &gemfire.PutStreamRequest{Config: &gemfire.StreamConfig{RegionName: region}}
	flush := func() error {
		if err := stream.Send(msg); err != nil {
			_, err = stream.CloseAndRecv()
			if err == nil {
				err = errors.New("stream closed by server")
			}
			return err
		}
		msg = &gemfire.PutStreamRequest{}
		return nil
	}
	for i := range count {
		msg.Entries = append(msg.Entries, &gemfire.Entry{Key: key(personID(i)), Value: packed(person(i))})
		if len(msg.Entries) == 500 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if len(msg.Entries) > 0 || msg.Config != nil {
		if err := flush(); err != nil {
			return err
		}
	}
	_, err = stream.CloseAndRecv()
	return err
}
