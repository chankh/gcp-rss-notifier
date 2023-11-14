package function

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"cloud.google.com/go/firestore"
	"github.com/GoogleCloudPlatform/functions-framework-go/functions"
	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/cloudevents/sdk-go/v2/event"
)

var projectID string
var collection string

// MessagePublishedData contains the full Pub/Sub message
// See the documentation for more details:
// https://cloud.google.com/eventarc/docs/cloudevents#pubsub
type MessagePublishedData struct {
	Message PubSubMessage
}

// PubSubMessage is the payload of a Pub/Sub event.
// See the documentation for more details:
// https://cloud.google.com/pubsub/docs/reference/rest/v1/PubsubMessage
type PubSubMessage struct {
	Data []byte `json:"data"`
}

type FeedItem struct {
	NotifyURL string `json:"notify"`
	Feed      string `json:"feed"`
	ID        string `json:"id"`
	Updated   string `json:"updated"`
	Link      string `json:"link"`
	Title     string `json:"title"`
	Content   string `json:"content"`
}

func init() {
	functions.CloudEvent("ProcessItem", processItem)
	projectID = os.Getenv("PROJECT_ID")
	collection = os.Getenv("COLLECTION")
}

func processItem(ctx context.Context, e event.Event) error {
	var msg MessagePublishedData
	if err := e.DataAs(&msg); err != nil {
		return fmt.Errorf("event.DataAs: %v", err)
	}

	item := FeedItem{}
	err := json.Unmarshal(msg.Message.Data, &item)
	if err != nil {
		return fmt.Errorf("failed parsing JSON: %v", err)
	}

	err = notify(item)
	if err != nil {
		return fmt.Errorf("failed sending notification: %v", err)
	}

	err = save(ctx, item)
	if err != nil {
		return fmt.Errorf("failed updating record: %v", err)
	}
	return nil
}

func notify(item FeedItem) error {
	text, err := htmlToMarkdown(item.Content)
	if err != nil {
		return fmt.Errorf("failed converting to markdown: %v", err)
	}

	// Add title and link before contents
	text = fmt.Sprintf("%s <%s|%s>\n\n%s", item.Feed, item.Link, item.Title, text)

	// Trim text if more than 4000 chars
	if len(text) > 4000 {
		text = text[:4000]
	}

	msg := make(map[string]string)
	msg["text"] = text
	jsonBody, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("json.Marshal: %v", err)
	}

	resp, err := http.Post(item.NotifyURL, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("error making http request: %v", err)
	}

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("response status: %s, %v", resp.Status, string(b))
	}

	return nil
}

func htmlToMarkdown(html string) (string, error) {
	opt := &md.Options{
		StrongDelimiter: "*",
	}
	converter := md.NewConverter("", true, opt)

	return converter.ConvertString(html)
}

func save(ctx context.Context, item FeedItem) error {
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		return fmt.Errorf("firestore client error: %v", err)
	}
	defer client.Close()

	wr, err := client.Collection(collection).Doc(item.ID).Set(ctx, map[string]string{
		"id":         item.ID,
		"lastUpdate": item.Updated,
		"title":      item.Title,
		"content":    item.Content,
		"link":       item.Link,
	}, firestore.MergeAll)
	if err != nil {
		return fmt.Errorf("failed writing to firestore: %v", err)
	}

	fmt.Printf("record updated, id: %s, timestamp: %s\n", item.ID, wr.UpdateTime)
	return nil
}
