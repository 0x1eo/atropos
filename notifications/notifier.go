package notifications

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"time"
)

type NotificationConfig struct {
	Enabled bool           `json:"enabled"`
	Webhook *WebhookConfig `json:"webhook,omitempty"`
	Email   *EmailConfig   `json:"email,omitempty"`
}

type WebhookConfig struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Retries int               `json:"retries"`
}

type EmailConfig struct {
	SMTPHost     string   `json:"smtp_host"`
	SMTPPort     int      `json:"smtp_port"`
	SMTPUser     string   `json:"smtp_user"`
	SMTPPassword string   `json:"smtp_password"`
	From         string   `json:"from"`
	To           []string `json:"to"`
}

type Notifier interface {
	Notify(event *CutEvent) error
}

type CutEvent struct {
	ID        string                 `json:"id"`
	Node      string                 `json:"node"`
	Action    string                 `json:"action"`
	Success   bool                   `json:"success"`
	Entropy   float64                `json:"entropy"`
	LatencyMs int64                  `json:"latency_ms"`
	Error     string                 `json:"error,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

type WebhookNotifier struct {
	config *WebhookConfig
	client *http.Client
}

func NewWebhookNotifier(config *WebhookConfig) *WebhookNotifier {
	return &WebhookNotifier{
		config: config,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (wn *WebhookNotifier) Notify(event *CutEvent) error {
	if wn.config == nil || wn.config.URL == "" {
		return nil
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	req, err := http.NewRequest("POST", wn.config.URL, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for key, value := range wn.config.Headers {
		req.Header.Set(key, value)
	}

	retries := wn.config.Retries
	if retries == 0 {
		retries = 3
	}

	var lastErr error
	for i := 0; i < retries; i++ {
		resp, err := wn.client.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(i+1) * time.Second)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}

		lastErr = fmt.Errorf("webhook returned status %d", resp.StatusCode)
		time.Sleep(time.Duration(i+1) * time.Second)
	}

	return fmt.Errorf("webhook failed after %d retries: %w", retries, lastErr)
}

type EmailNotifier struct {
	config *EmailConfig
}

func NewEmailNotifier(config *EmailConfig) *EmailNotifier {
	return &EmailNotifier{
		config: config,
	}
}

func (en *EmailNotifier) Notify(event *CutEvent) error {
	if en.config == nil || len(en.config.To) == 0 {
		return nil
	}

	subject := fmt.Sprintf("[Atropos] Cut %s - %s",
		map[bool]string{true: "Success", false: "Failed"}[event.Success],
		event.Node)

	body := fmt.Sprintf(`
Atropos Cut Notification

Node: %s
Action: %s
Status: %s
Entropy: %.4f
Latency: %dms
Timestamp: %s
`, event.Node, event.Action,
		map[bool]string{true: "SUCCESS", false: "FAILED"}[event.Success],
		event.Entropy, event.LatencyMs,
		event.Timestamp.Format(time.RFC3339))

	if !event.Success && event.Error != "" {
		body += fmt.Sprintf("\nError: %s\n", event.Error)
	}

	auth := smtp.PlainAuth("", en.config.SMTPUser, en.config.SMTPPassword, "")

	msg := fmt.Sprintf("From: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		en.config.From, subject, body)

	err := smtp.SendMail(
		fmt.Sprintf("%s:%d", en.config.SMTPHost, en.config.SMTPPort),
		auth,
		en.config.From,
		en.config.To,
		[]byte(msg),
	)
	if err != nil {
		return fmt.Errorf("send email: %w", err)
	}

	return nil
}

type CompositeNotifier struct {
	notifiers []Notifier
}

func NewCompositeNotifier(notifiers []Notifier) *CompositeNotifier {
	return &CompositeNotifier{
		notifiers: notifiers,
	}
}

func (cn *CompositeNotifier) Notify(event *CutEvent) error {
	var errors []error
	for _, notifier := range cn.notifiers {
		if err := notifier.Notify(event); err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("notifications failed: %v", errors)
	}

	return nil
}

type NotificationManager struct {
	config   *NotificationConfig
	notifier Notifier
}

func NewNotificationManager(config *NotificationConfig) *NotificationManager {
	if !config.Enabled {
		return &NotificationManager{
			config: config,
		}
	}

	var notifiers []Notifier

	if config.Webhook != nil {
		notifiers = append(notifiers, NewWebhookNotifier(config.Webhook))
	}

	if config.Email != nil {
		notifiers = append(notifiers, NewEmailNotifier(config.Email))
	}

	return &NotificationManager{
		config:   config,
		notifier: NewCompositeNotifier(notifiers),
	}
}

func (nm *NotificationManager) NotifyCut(event *CutEvent) error {
	if !nm.config.Enabled {
		return nil
	}

	event.Metadata = map[string]interface{}{
		"source": "atropos",
	}

	return nm.notifier.Notify(event)
}
