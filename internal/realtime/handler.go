package realtime

import (
	"errors"
	"math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// heartbeatInterval is how often a heartbeat is sent and the session is checked.
// Proxies usually cut a connection without traffic after a minute (Nginx, by default); 25
// seconds is well below that.
var heartbeatInterval = 25 * time.Second

// maxStreamLifetime is how long a stream lasts, on average. When it ends, the browser
// reconnects on its own, and on reconnecting the session and the permissions of each topic are
// checked again: that way, someone who lost access to a topic stops listening to it even if
// nobody disconnects them.
var maxStreamLifetime = 15 * time.Minute

// streamLifetime returns the life of a stream: maxStreamLifetime ± 2/15 at random (between 13
// and 17 minutes). After a restart every tab connects at once; with the variation, their
// renewals spread out instead of all arriving together to ask for their state.
func streamLifetime() time.Duration {
	spread := maxStreamLifetime * 2 / 15
	return maxStreamLifetime - spread + rand.N(2*spread+1)
}

// Sessions is what the stream needs from auth (auth.Service implements it): to know which
// session it was opened with, and to check on every heartbeat that it is still valid (not
// closed or expired).
type Sessions interface {
	SessionKey(r *http.Request) string
	CheckSession(r *http.Request) error
}

// maxTopics is how many topics a connection can listen to.
const maxTopics = 20

// Handler answers GET /events: it opens an SSE stream with the events of the user and of the
// topics in ?topics=project:42,board:7 they are allowed to listen to. It goes behind RequireUser.
//
// The first event is "ready": {"topics": [the ones it listens to], "rejected": [the ones it is
// not allowed to]}. A rejected topic does not cut the connection: the user still gets their
// channel, and the frontend knows which topic it lost (for example, because the user was taken
// out of a project). On reconnecting, the frontend has to ask for the state again through REST:
// the events sent in between are not resent.
//
// The stream is cut when its session is closed: right away if auth says so (SessionEnded,
// UserSessionsEnded), and otherwise on the next heartbeat. And it lasts at most maxStreamLifetime.
func (b *Broker) Handler(sessions Sessions) http.Handler {
	return httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		user := auth.MustUser(r.Context())

		topics, err := parseTopics(r.URL.Query().Get("topics"))
		if err != nil {
			return err
		}
		allowed, rejected, err := b.authorizeTopics(r.Context(), user.ID, topics)
		if err != nil {
			return err
		}

		sub, err := b.subscribe(user.ID, sessions.SessionKey(r), allowed)
		switch {
		case errors.Is(err, ErrTooManyConnections):
			w.Header().Set("Retry-After", "30")
			return &httpx.Error{Status: http.StatusTooManyRequests, Code: "too_many_connections"}
		case errors.Is(err, ErrClosed):
			return &httpx.Error{Status: http.StatusServiceUnavailable, Code: "shutting_down"}
		case err != nil:
			return err
		}
		defer b.unsubscribe(sub)

		stream, err := Open(w, r)
		if err != nil {
			return err
		}
		if err := stream.Send(Event{Type: "ready", Data: readyData{Topics: allowed, Rejected: rejected}}); err != nil {
			return nil // the client is already gone
		}

		heartbeat := time.NewTicker(heartbeatInterval)
		defer heartbeat.Stop()
		lifetime := time.NewTimer(streamLifetime())
		defer lifetime.Stop()
		for {
			select {
			case <-r.Context().Done():
				return nil // the client closed the connection
			case <-sub.closed:
				return nil // session closed, slow client or the API is shutting down
			case <-lifetime.C:
				return nil // the browser reconnects, and everything is checked again then
			case ev := <-sub.events:
				if err := stream.Send(ev); err != nil {
					return nil
				}
			case <-heartbeat.C:
				// A closed or expired session cuts the stream. On reconnecting it gets 401, and the browser's
				// EventSource stops trying (the frontend sees it in onerror).
				if err := sessions.CheckSession(r); err != nil {
					return nil
				}
				if err := stream.Comment("ping"); err != nil {
					return nil
				}
			}
		}
	})
}

// readyData is the content of the ready event.
type readyData struct {
	Topics   []string `json:"topics"`
	Rejected []string `json:"rejected"`
}

// parseTopics reads the list of topics, without repeats.
func parseTopics(raw string) ([]string, error) {
	var topics []string
	for t := range strings.SplitSeq(raw, ",") {
		t = strings.TrimSpace(t)
		if t != "" && !slices.Contains(topics, t) {
			topics = append(topics, t)
		}
	}
	if len(topics) > maxTopics {
		return nil, httpx.BadRequest("too_many_topics", "max", maxTopics)
	}
	return topics, nil
}
