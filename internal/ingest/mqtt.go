// Copyright (c) 2026 Vassel Goleyu. MIT License.

package ingest

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
)

// MQTTConfig configures the subscriber.
type MQTTConfig struct {
	URL            string // tcp://host:1883
	ClientID       string // stable: the persistent session is keyed by it
	Username       string
	Password       string
	TopicFilter    string // default spillway/v1/+/telemetry
	ReceiveMaximum uint16 // max unacknowledged messages the broker may have in flight (bounds memory)
	SessionExpiry  uint32 // seconds the broker keeps the session (and queued QoS 1 messages) while we are away
}

// Subscriber receives telemetry over MQTT v5 with a persistent session and QoS 1. Messages are
// acknowledged manually and only after the reading is committed (or permanently rejected), so a
// crash or restart makes the broker redeliver everything that was not yet safe: at-least-once
// delivery, made exactly-once in storage by the dedupe layers. Failed messages stay unacknowledged
// and come back on the next session (reconnect or restart).
type Subscriber struct {
	cm     *autopaho.ConnectionManager
	pipe   *Pipeline
	log    *slog.Logger
	filter string
	up     atomic.Bool
	ctx    context.Context //nolint:containedctx // the connection's lifetime
	cancel context.CancelFunc
}

// StartSubscriber connects (and keeps reconnecting) in the background.
func StartSubscriber(ctx context.Context, cfg MQTTConfig, p *Pipeline, log *slog.Logger) (*Subscriber, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse MQTT URL: %w", err)
	}
	if cfg.TopicFilter == "" {
		cfg.TopicFilter = "spillway/v1/+/telemetry"
	}
	if cfg.ReceiveMaximum == 0 {
		cfg.ReceiveMaximum = 512
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Subscriber{pipe: p, log: log, filter: cfg.TopicFilter, ctx: ctx, cancel: cancel}
	rm := cfg.ReceiveMaximum

	acfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{u},
		KeepAlive:                     30,
		CleanStartOnInitialConnection: false,
		SessionExpiryInterval:         cfg.SessionExpiry,
		ConnectUsername:               cfg.Username,
		ConnectPassword:               []byte(cfg.Password),
		ConnectTimeout:                10 * time.Second,
		ConnectPacketBuilder: func(c *paho.Connect, _ *url.URL) (*paho.Connect, error) {
			if c.Properties == nil {
				c.Properties = &paho.ConnectProperties{}
			}
			c.Properties.ReceiveMaximum = &rm
			return c, nil
		},
		OnConnectionUp: func(cm *autopaho.ConnectionManager, _ *paho.Connack) {
			s.up.Store(true)
			go func() { // must not block the connection manager
				if _, err := cm.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: s.filter, QoS: 1}}}); err != nil {
					log.Error("mqtt subscribe failed", "err", err)
				}
			}()
		},
		OnConnectionDown: func() bool { s.up.Store(false); log.Warn("mqtt connection lost, reconnecting"); return true },
		OnConnectError:   func(err error) { log.Warn("mqtt connect failed", "err", err) },
		ClientConfig: paho.ClientConfig{
			ClientID:                   cfg.ClientID,
			EnableManualAcknowledgment: true,
			SendAcksInterval:           20 * time.Millisecond,
			OnPublishReceived:          []func(paho.PublishReceived) (bool, error){s.onPublish},
			OnClientError:              func(err error) { log.Warn("mqtt client error", "err", err) },
		},
	}
	cm, err := autopaho.NewConnection(ctx, acfg)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("start MQTT connection: %w", err)
	}
	s.cm = cm
	return s, nil
}

func deviceFromTopic(topic string) (string, bool) {
	parts := strings.Split(topic, "/")
	if len(parts) == 4 && parts[0] == "spillway" && parts[1] == "v1" && parts[3] == "telemetry" && parts[2] != "" {
		return parts[2], true
	}
	return "", false
}

func (s *Subscriber) onPublish(pr paho.PublishReceived) (bool, error) {
	pk := pr.Packet
	ack := func() {
		if err := pr.Client.Ack(pk); err != nil {
			s.log.Warn("mqtt ack failed", "err", err) // the broker will redeliver; dedupe absorbs it
		}
	}
	dev, ok := deviceFromTopic(pk.Topic)
	if !ok {
		s.pipe.m.Received.WithLabelValues("mqtt").Inc()
		s.pipe.m.Rejected.WithLabelValues(ReasonTopicMismatch).Inc()
		ack()
		return true, nil
	}
	msg := &Message{Payload: pk.Payload, TopicDevice: dev, Source: "mqtt", Done: func(o Outcome) {
		if o.Kind == Failed {
			s.log.Warn("message not acknowledged, will be redelivered", "err", o.Err)
			return
		}
		ack() // committed, a duplicate of committed data, or permanently rejected
	}}
	if err := s.pipe.Submit(s.ctx, msg, true); err != nil {
		s.log.Warn("message not accepted, left unacknowledged", "err", err)
	}
	return true, nil
}

// Connected reports whether the broker connection is up (readiness).
func (s *Subscriber) Connected() error {
	if !s.up.Load() {
		return fmt.Errorf("mqtt not connected")
	}
	return nil
}

// StopIntake unsubscribes so no new messages arrive, while the connection stays up to send the
// acknowledgements of messages still being drained. Call it before Pipeline.Close.
func (s *Subscriber) StopIntake(ctx context.Context) error {
	if _, err := s.cm.Unsubscribe(ctx, &paho.Unsubscribe{Topics: []string{s.filter}}); err != nil {
		return fmt.Errorf("mqtt unsubscribe: %w", err)
	}
	return nil
}

// Close disconnects and waits for the connection manager to finish.
func (s *Subscriber) Close(ctx context.Context) error {
	err := s.cm.Disconnect(ctx)
	s.cancel()
	select {
	case <-s.cm.Done():
	case <-ctx.Done():
	}
	if err != nil {
		return fmt.Errorf("mqtt disconnect: %w", err)
	}
	return nil
}
