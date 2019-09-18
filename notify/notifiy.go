package notify

import (
	"context"
	"fmt"
	"github.com/feynman-go/workshop/syncrun"
	"github.com/feynman-go/workshop/syncrun/prob"
	"github.com/feynman-go/workshop/window"
	"github.com/pkg/errors"
	"log"
	"net/textproto"
	"sync"
	"time"
)

type Publisher interface {
	Publish(ctx context.Context, message []Message) (lastToken string)
}

type Notifier struct {
	pb         *prob.Prob
	stream     MessageStream
	whiteBoard WhiteBoard
	publisher  Publisher
	ag         *aggergator
	triggers   []window.Trigger
}

type Option struct {
	MaxCount int64
	MaxDuration time.Duration
}

func New(stream MessageStream, whiteBoard WhiteBoard, publisher Publisher, option Option) *Notifier {
	ret := &Notifier {
		stream: stream,
		whiteBoard: whiteBoard,
		ag: &aggergator{
			publisher: publisher,
		},

	}
	ret.pb = prob.New(ret.run)

	var triggers []window.Trigger
	if option.MaxCount <= 0 {
		option.MaxCount = 1
	}
	triggers = append(triggers, window.NewCounterTrigger(uint64(option.MaxCount)))

	if option.MaxDuration > 0 {
		triggers = append(triggers, window.NewDurationTrigger(option.MaxDuration))
	}

	return ret
}

func (notifier *Notifier) Start(ctx context.Context, restartMax time.Duration, restartMin time.Duration) error {
	notifier.pb.Start()
	return nil
}

func (notifier *Notifier) run(ctx context.Context) {
	f := syncrun.FuncWithRandomStart(func(ctx context.Context) bool {
		token, err := notifier.whiteBoard.GetResumeToken(ctx)
		if err != nil {
			return true
		}

		cursor, err := notifier.stream.ResumeFromToken(ctx, token)
		if err != nil {
			return true
		}

		defer func() {
			closeCtx, _ := context.WithCancel(context.Background())
			err = cursor.Close(closeCtx)
			if err != nil {
				log.Println("cursor close err:", err)
			}
		}()

		notifier.ag.Reset()
		wd := window.New(notifier.ag, notifier.triggers)

		for msg := cursor.Next(ctx); msg != nil; msg = cursor.Next(ctx) {
			err = wd.Accept(ctx, *msg)
			if err != nil {
				log.Println("push message err:", err)
				break
			}
		}
		return true
	}, syncrun.RandRestart(time.Second, 5 * time.Second))

	f(ctx)
}

func (notifier *Notifier) Close() error {
	notifier.pb.Stop()
	return nil
}

func (notifier *Notifier) Closed() chan <- struct{} {
	return notifier.pb.Stopped()
}

type Message struct {
	ID string
	PayLoad interface{}
	Head textproto.MIMEHeader
	Token string
}

type Notify struct {
	CursorID string
}

type WhiteBoard interface {
	StoreResumeToken(ctx context.Context, token string) error
	GetResumeToken(ctx context.Context) (token string, err error)
}

type Cursor interface {
	Next(ctx context.Context) *Message
	Close(ctx context.Context) error
	Err() error
	ResumeToken() string
}

type MessageStream interface {
	ResumeFromToken(ctx context.Context, resumeToken string) (Cursor, error)
}

type aggergator struct {
	rw sync.RWMutex
	msgs []Message
	seq uint64
	publisher Publisher
	wb WhiteBoard
	lastErr error
}

func (agg *aggergator) Aggregate(ctx context.Context, item window.Whiteboard, input interface{}) (err error) {
	agg.rw.Lock()
	defer agg.rw.Unlock()

	if agg.lastErr != nil {
		return fmt.Errorf("has last err: %v", agg.lastErr)
	}

	msg, ok := input.(Message)
	if !ok {
		return errors.New("input must be message")
	}
	agg.msgs = append(agg.msgs, msg)
	return nil
}

func (agg *aggergator) Reset() {
	agg.rw.Lock()
	defer agg.rw.Unlock()

	agg.lastErr = nil
	agg.msgs = agg.msgs[:0]
}

func (agg *aggergator) Trigger(ctx context.Context, acceptErr error, nextSeq uint64) error {
	if acceptErr != nil {
		return fmt.Errorf("accept err: %v", acceptErr)
	}
	agg.rw.Lock()
	defer agg.rw.Unlock()

	if agg.lastErr == nil {
		if len(agg.msgs) != 0 {
			lastToken := agg.publisher.Publish(ctx, agg.msgs)
			if lastToken == "" {
				agg.lastErr = errors.New("not support")
			} else {
				err := agg.wb.StoreResumeToken(ctx, lastToken)
				if err == nil {
					if lastToken != agg.msgs[len(agg.msgs) - 1].Token {
						agg.lastErr = errors.New("bad message notify")
					}
				} else {
					agg.lastErr = fmt.Errorf("store resume token err: %v", err)
				}
			}
		}
		if agg.msgs != nil {
			agg.msgs = agg.msgs[:0]
		}
	}
	agg.seq = nextSeq
	return nil
}