// Package views retrieves data and controls which of it is visible.
//
// This is the only package that should interact directly with Twilio - all
// other code should talk to this package to determine whether a particular
// piece of information should be visible, or not.
package views

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang/groupcache/singleflight"
	log "github.com/inconshreveable/log15"
	"github.com/kevinburke/logrole/cache"
	"github.com/kevinburke/logrole/config"
	"github.com/kevinburke/logrole/services"
	"github.com/kevinburke/rest"
	twilio "github.com/kevinburke/twilio-go"
)

// Front page of messages should be changing
var frontPageTimeout = 30 * time.Second

// Next page == all resources "older" than a resource that exists, less likely
// to be changing.
var nextPageTimeout = 5 * time.Minute

// A Client retrieves resources from a backend API, and hides information that
// shouldn't be seen before returning them to the caller.
type Client interface {
	SetBasicAuth(r *http.Request)
	GetMessage(context.Context, *config.User, string) (*Message, error)
	GetCall(context.Context, *config.User, string) (*Call, error)
	GetConference(context.Context, *config.User, string) (*Conference, error)
	GetIncomingNumber(ctx context.Context, u *config.User, sid string) (*IncomingNumber, error)
	GetIncomingNumberByPN(ctx context.Context, u *config.User, pn string) (*IncomingNumber, error)
	GetAlert(context.Context, *config.User, string) (*Alert, error)
	GetMediaURLs(context.Context, *config.User, string) ([]*url.URL, error)
	GetMessagePageInRange(context.Context, *config.User, time.Time, time.Time, url.Values) (*MessagePage, uint64, error)
	GetCallPageInRange(context.Context, *config.User, time.Time, time.Time, url.Values) (*CallPage, uint64, error)
	GetNumberPage(context.Context, *config.User, url.Values) (*IncomingNumberPage, uint64, error)
	GetConferencePageInRange(context.Context, *config.User, time.Time, time.Time, url.Values) (*ConferencePage, uint64, error)
	GetAlertPageInRange(context.Context, *config.User, time.Time, time.Time, url.Values) (*AlertPage, uint64, error)
	GetNextMessagePageInRange(context.Context, *config.User, time.Time, time.Time, string) (*MessagePage, uint64, error)
	GetNextNumberPage(context.Context, *config.User, string) (*IncomingNumberPage, uint64, error)
	GetNextCallPageInRange(context.Context, *config.User, time.Time, time.Time, string) (*CallPage, uint64, error)
	GetNextConferencePageInRange(context.Context, *config.User, time.Time, time.Time, string) (*ConferencePage, uint64, error)
	GetNextAlertPageInRange(context.Context, *config.User, time.Time, time.Time, string) (*AlertPage, uint64, error)
	GetNextRecordingPage(context.Context, *config.User, string) (*RecordingPage, error)
	GetCallRecordings(context.Context, *config.User, string, url.Values) (*RecordingPage, error)
	GetCallAlerts(context.Context, *config.User, string) (*AlertPage, error)
	CacheCommonQueries(uint, <-chan bool)
	IsTwilioNumber(num twilio.PhoneNumber) bool
}

type client struct {
	log.Logger
	group      singleflight.Group
	cache      *cache.Cache
	client     *twilio.Client
	secretKey  *[32]byte
	permission *config.Permission
	numbers    map[twilio.PhoneNumber]bool
	numbersMu  sync.RWMutex
}

// this allows about 8k entries in the cache
const cacheSizeMB = 25
const averageCacheEntryBytes = 3000

// NewClient creates a new Client encapsulating the provided values.
func NewClient(l log.Logger, c *twilio.Client, secretKey *[32]byte, p *config.Permission) Client {
	return &client{
		Logger:     l,
		group:      singleflight.Group{},
		cache:      cache.NewCache(cacheSizeMB*1024*1024/averageCacheEntryBytes, l),
		client:     c,
		secretKey:  secretKey,
		permission: p,
	}
}

func (vc *client) getNumbers() {
	iter := vc.client.IncomingNumbers.GetPageIterator(nil)
	size, count := 0, 0
	mp := make(map[twilio.PhoneNumber]bool)
	for count < 200 {
		page, err := iter.Next(context.Background())
		if err == twilio.NoMoreResults {
			break
		}
		if err != nil {
			return
		}
		for _, pn := range page.IncomingPhoneNumbers {
			mp[pn.PhoneNumber] = true
			size++
		}
		count++
	}
	vc.numbersMu.Lock()
	vc.numbers = mp
	vc.numbersMu.Unlock()
	vc.Debug("Updated phone number map", "size", size)
}

// SetBasicAuth sets the Twilio AccountSid and AuthToken on the given request.
func (vc *client) SetBasicAuth(r *http.Request) {
	r.SetBasicAuth(vc.client.AccountSid, vc.client.AuthToken)
}

// GetMessage fetches a single Message from the Twilio API, and returns any
// network or permission errors that occur.
func (vc *client) GetMessage(ctx context.Context, user *config.User, sid string) (*Message, error) {
	message, err := vc.client.Messages.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	return NewMessage(message, vc.permission, user)
}

// GetCall fetches a single Call from the Twilio API, and returns any
// network or permission errors that occur.
func (vc *client) GetCall(ctx context.Context, user *config.User, sid string) (*Call, error) {
	call, err := vc.client.Calls.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	return NewCall(call, vc.permission, user)
}

// GetAlert fetches a single Alert from the Twilio API, and returns any
// network or permission errors that occur.
func (vc *client) GetAlert(ctx context.Context, user *config.User, sid string) (*Alert, error) {
	call, err := vc.client.Monitor.Alerts.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	return NewAlert(call, vc.permission, user)
}

// GetIncomingNumber fetches a single IncomingNumber from the Twilio API, and
// returns any network or permission errors that occur.
func (vc *client) GetIncomingNumber(ctx context.Context, user *config.User, sid string) (*IncomingNumber, error) {
	number, err := vc.client.IncomingNumbers.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	return NewIncomingNumber(number, vc.permission, user)
}

// GetIncomingNumberByPN fetches a single IncomingNumber from the Twilio API, and
// returns any network or permission errors that occur.
func (vc *client) GetIncomingNumberByPN(ctx context.Context, user *config.User, pn string) (*IncomingNumber, error) {
	data := url.Values{"PhoneNumber": []string{pn}}
	page, err := vc.client.IncomingNumbers.GetPage(ctx, data)
	if err != nil {
		return nil, err
	}
	if len(page.IncomingPhoneNumbers) != 1 {
		return nil, &rest.Error{
			Status: 404,
			Title:  fmt.Sprintf("Phone number %s not found, or too many numbers match that result", pn),
		}
	}
	return NewIncomingNumber(page.IncomingPhoneNumbers[0], vc.permission, user)
}

// GetConference fetches a single Conference from the Twilio API, and returns any
// network or permission errors that occur.
func (vc *client) GetConference(ctx context.Context, user *config.User, sid string) (*Conference, error) {
	conference, err := vc.client.Conferences.Get(ctx, sid)
	if err != nil {
		return nil, err
	}
	return NewConference(conference, vc.permission, user)
}

// Just make sure we get all of the media when we make a request
var mediaUrlsFilters = url.Values{
	"PageSize": []string{"100"},
}

// GetMediaURLs retrieves all media URL's for a given client, but encrypts and
// obscures them behind our image proxy first.
func (vc *client) GetMediaURLs(ctx context.Context, u *config.User, sid string) ([]*url.URL, error) {
	if u.CanViewMedia() == false {
		return nil, config.PermissionDenied
	}
	urls, err := vc.client.Messages.GetMediaURLs(ctx, sid, mediaUrlsFilters)
	if err != nil {
		return nil, err
	}
	opaqueImages := make([]*url.URL, len(urls))
	for i, u := range urls {
		enc := services.Opaque(u.String(), vc.secretKey)
		opaqueURL, err := url.Parse("/images/" + enc)
		if err != nil {
			return nil, err
		}
		opaqueImages[i] = opaqueURL
	}
	return opaqueImages, nil
}

func hash(typ, val string, a, b time.Time) string {
	return strings.Join([]string{typ, val, a.Format(time.RFC3339Nano), b.Format(time.RFC3339Nano)}, "|")
}

func (vc *client) getAndCacheMessage(ctx context.Context, start, end time.Time, data url.Values) (*CacheResult, error) {
	page, err := vc.client.Messages.GetMessagesInRange(start, end, data).Next(ctx)
	if err != nil {
		return nil, err
	}
	key := hash("messages", data.Encode(), start, end)
	vc.cache.Set(key, page, frontPageTimeout)
	return &CacheResult{Value: page}, nil
}

func (vc *client) getAndCacheConference(ctx context.Context, start, end time.Time, data url.Values) (*CacheResult, error) {
	page, err := vc.client.Conferences.GetConferencesInRange(start, end, data).Next(ctx)
	if err != nil {
		return nil, err
	}
	key := hash("conferences", data.Encode(), start, end)
	vc.cache.Set(key, page, frontPageTimeout)
	return &CacheResult{Value: page}, nil
}

func (vc *client) getAndCacheAlert(ctx context.Context, start, end time.Time, data url.Values) (*CacheResult, error) {
	page, err := vc.client.Monitor.Alerts.GetAlertsInRange(start, end, data).Next(ctx)
	if err != nil {
		return nil, err
	}
	key := hash("alerts", data.Encode(), start, end)
	vc.cache.Set(key, page, frontPageTimeout)
	return &CacheResult{Value: page}, nil
}

func (vc *client) getAndCacheCall(ctx context.Context, start, end time.Time, data url.Values) (*CacheResult, error) {
	page, err := vc.client.Calls.GetCallsInRange(start, end, data).Next(ctx)
	if err != nil {
		return nil, err
	}
	key := hash("calls", data.Encode(), start, end)
	vc.cache.Set(key, page, frontPageTimeout)
	return &CacheResult{Value: page}, nil
}

func (vc *client) getAndCacheNumber(ctx context.Context, data url.Values) (*CacheResult, error) {
	page, err := vc.client.IncomingNumbers.GetPageIterator(data).Next(ctx)
	if err != nil {
		return nil, err
	}
	key := hash("incoming-numbers", data.Encode(), twilio.Epoch, twilio.HeatDeath)
	vc.cache.Set(key, page, frontPageTimeout)
	return &CacheResult{Value: page}, nil
}

type CacheResult struct {
	Time  uint64
	Value interface{}
}

func (vc *client) cacheToMsg(user *config.User, val interface{}) (*MessagePage, uint64, error) {
	result, ok := val.(*CacheResult)
	if !ok {
		return nil, 0, errors.New("Could not cast fetch result to a CacheResult")
	}
	page, ok := result.Value.(*twilio.MessagePage)
	if !ok {
		return nil, 0, errors.New("Could not cast fetch result to a MessagePage")
	}
	mp, err := NewMessagePage(page, vc.permission, user)
	return mp, result.Time, err
}

func (vc *client) GetMessagePageInRange(ctx context.Context, user *config.User, start time.Time, end time.Time, data url.Values) (*MessagePage, uint64, error) {
	key := hash("messages", data.Encode(), start, end)
	val, err := vc.group.Do(key, func() (interface{}, error) {
		page := new(twilio.MessagePage)
		t, err := vc.cache.Get(key, page)
		if err == nil {
			return &CacheResult{t, page}, nil
		}
		return vc.getAndCacheMessage(ctx, start, end, data)
	})
	if err != nil {
		return nil, 0, err
	}
	return vc.cacheToMsg(user, val)
}

func (vc *client) GetNextMessagePageInRange(ctx context.Context, user *config.User, start time.Time, end time.Time, nextPage string) (*MessagePage, uint64, error) {
	key := hash("messages", nextPage, start, end)
	val, err := vc.group.Do(key, func() (interface{}, error) {
		page := new(twilio.MessagePage)
		t, err := vc.cache.Get(key, page)
		if err == nil {
			return &CacheResult{t, page}, nil
		}
		page, err = vc.client.Messages.GetNextMessagesInRange(start, end, nextPage).Next(ctx)
		if err != nil {
			return nil, err
		}
		vc.cache.Set(key, page, nextPageTimeout)
		return &CacheResult{Value: page}, nil
	})
	if err != nil {
		return nil, 0, err
	}
	return vc.cacheToMsg(user, val)
}

func (vc *client) cacheToCall(user *config.User, val interface{}) (*CallPage, uint64, error) {
	result, ok := val.(*CacheResult)
	if !ok {
		return nil, 0, errors.New("Could not cast fetch result to a CacheResult")
	}
	page, ok := result.Value.(*twilio.CallPage)
	if !ok {
		return nil, 0, errors.New("Could not cast fetch result to a CallPage")
	}
	cp, err := NewCallPage(page, vc.permission, user)
	return cp, result.Time, err
}

func (vc *client) GetCallPageInRange(ctx context.Context, user *config.User, start time.Time, end time.Time, data url.Values) (*CallPage, uint64, error) {
	key := hash("calls", data.Encode(), start, end)
	val, err := vc.group.Do(key, func() (interface{}, error) {
		page := new(twilio.CallPage)
		t, err := vc.cache.Get(key, page)
		if err == nil {
			return &CacheResult{t, page}, nil
		}
		return vc.getAndCacheCall(ctx, start, end, data)
	})
	if err != nil {
		return nil, 0, err
	}
	return vc.cacheToCall(user, val)
}

func (vc *client) GetNextCallPageInRange(ctx context.Context, user *config.User, start time.Time, end time.Time, nextPage string) (*CallPage, uint64, error) {
	key := hash("calls", nextPage, start, end)
	val, err := vc.group.Do(key, func() (interface{}, error) {
		page := new(twilio.CallPage)
		t, err := vc.cache.Get(key, page)
		if err == nil {
			return &CacheResult{Time: t, Value: page}, nil
		}
		page, err = vc.client.Calls.GetNextCallsInRange(start, end, nextPage).Next(ctx)
		if err != nil {
			return nil, err
		}
		vc.cache.Set(key, page, nextPageTimeout)
		return &CacheResult{Value: page}, nil
	})
	if err != nil {
		return nil, 0, err
	}
	return vc.cacheToCall(user, val)
}

func (vc *client) cacheToNumber(user *config.User, val interface{}) (*IncomingNumberPage, uint64, error) {
	result, ok := val.(*CacheResult)
	if !ok {
		return nil, 0, errors.New("Could not cast fetch result to a CacheResult")
	}
	page, ok := result.Value.(*twilio.IncomingPhoneNumberPage)
	if !ok {
		return nil, 0, errors.New("Could not cast fetch result to a CallPage")
	}
	np, err := NewIncomingNumberPage(page, vc.permission, user)
	return np, result.Time, err
}

func (vc *client) GetNumberPage(ctx context.Context, user *config.User, data url.Values) (*IncomingNumberPage, uint64, error) {
	key := hash("incoming-numbers", data.Encode(), twilio.Epoch, twilio.HeatDeath)
	val, err := vc.group.Do(key, func() (interface{}, error) {
		page := new(twilio.IncomingPhoneNumberPage)
		t, err := vc.cache.Get(key, page)
		if err == nil {
			return &CacheResult{t, page}, nil
		}
		return vc.getAndCacheNumber(ctx, data)
	})
	if err != nil {
		return nil, 0, err
	}
	return vc.cacheToNumber(user, val)
}

func (vc *client) GetNextNumberPage(ctx context.Context, user *config.User, nextPage string) (*IncomingNumberPage, uint64, error) {
	key := hash("incoming-numbers", nextPage, twilio.Epoch, twilio.HeatDeath)
	val, err := vc.group.Do(key, func() (interface{}, error) {
		page := new(twilio.IncomingPhoneNumberPage)
		t, err := vc.cache.Get(key, page)
		if err == nil {
			return &CacheResult{Time: t, Value: page}, nil
		}
		if err = vc.client.GetNextPage(ctx, nextPage, page); err != nil {
			return nil, err
		}
		vc.cache.Set(key, page, nextPageTimeout)
		return &CacheResult{Value: page}, nil
	})
	if err != nil {
		return nil, 0, err
	}
	return vc.cacheToNumber(user, val)
}

func (vc *client) cacheToConference(user *config.User, val interface{}) (*ConferencePage, uint64, error) {
	result, ok := val.(*CacheResult)
	if !ok {
		return nil, 0, errors.New("Could not cast fetch result to a CacheResult")
	}
	page, ok := result.Value.(*twilio.ConferencePage)
	if !ok {
		return nil, 0, errors.New("Could not cast fetch result to a ConferencePage")
	}
	cp, err := NewConferencePage(page, vc.permission, user)
	return cp, result.Time, err
}

func (vc *client) GetConferencePageInRange(ctx context.Context, user *config.User, start time.Time, end time.Time, data url.Values) (*ConferencePage, uint64, error) {
	key := hash("conferences", data.Encode(), start, end)
	val, err := vc.group.Do(key, func() (interface{}, error) {
		page := new(twilio.ConferencePage)
		t, err := vc.cache.Get(key, page)
		if err == nil {
			return &CacheResult{t, page}, nil
		}
		page, err = vc.client.Conferences.GetConferencesInRange(start, end, data).Next(ctx)
		if err != nil {
			return nil, err
		}
		vc.cache.Set(key, page, nextPageTimeout)
		return &CacheResult{Value: page}, nil
	})
	if err != nil {
		return nil, 0, err
	}
	return vc.cacheToConference(user, val)
}

func (vc *client) GetNextConferencePageInRange(ctx context.Context, user *config.User, start time.Time, end time.Time, nextPage string) (*ConferencePage, uint64, error) {
	key := hash("conferences", nextPage, start, end)
	val, err := vc.group.Do(key, func() (interface{}, error) {
		page := new(twilio.ConferencePage)
		t, err := vc.cache.Get(key, page)
		if err == nil {
			return &CacheResult{t, page}, nil
		}
		page, err = vc.client.Conferences.GetNextConferencesInRange(start, end, nextPage).Next(ctx)
		if err != nil {
			return nil, err
		}
		vc.cache.Set(key, page, nextPageTimeout)
		return &CacheResult{Value: page}, nil
	})
	if err != nil {
		return nil, 0, err
	}
	return vc.cacheToConference(user, val)
}

func (vc *client) cacheToAlert(user *config.User, val interface{}) (*AlertPage, uint64, error) {
	result, ok := val.(*CacheResult)
	if !ok {
		return nil, 0, errors.New("Could not cast fetch result to a CacheResult")
	}
	page, ok := result.Value.(*twilio.AlertPage)
	if !ok {
		return nil, 0, errors.New("Could not cast fetch result to a AlertPage")
	}
	ap, err := NewAlertPage(page, vc.permission, user)
	return ap, result.Time, err
}

func (vc *client) GetAlertPageInRange(ctx context.Context, user *config.User, start time.Time, end time.Time, data url.Values) (*AlertPage, uint64, error) {
	key := hash("alerts", data.Encode(), start, end)
	val, err := vc.group.Do(key, func() (interface{}, error) {
		page := new(twilio.AlertPage)
		t, err := vc.cache.Get(key, page)
		if err == nil {
			return &CacheResult{t, page}, nil
		}
		return vc.getAndCacheAlert(ctx, start, end, data)
	})
	if err != nil {
		return nil, 0, err
	}
	return vc.cacheToAlert(user, val)
}

func (vc *client) GetNextAlertPageInRange(ctx context.Context, user *config.User, start time.Time, end time.Time, nextPage string) (*AlertPage, uint64, error) {
	key := hash("alerts", nextPage, start, end)
	val, err := vc.group.Do(key, func() (interface{}, error) {
		page := new(twilio.AlertPage)
		t, err := vc.cache.Get(key, page)
		if err == nil {
			return &CacheResult{t, page}, nil
		}
		page, err = vc.client.Monitor.Alerts.GetNextAlertsInRange(start, end, nextPage).Next(ctx)
		if err != nil {
			return nil, err
		}
		vc.cache.Set(key, page, nextPageTimeout)
		return &CacheResult{Value: page}, nil
	})
	if err != nil {
		return nil, 0, err
	}
	return vc.cacheToAlert(user, val)
}

func (vc *client) GetNextRecordingPage(ctx context.Context, user *config.User, nextPage string) (*RecordingPage, error) {
	page := new(twilio.RecordingPage)
	err := vc.client.GetNextPage(ctx, nextPage, page)
	if err != nil {
		return nil, err
	}
	return NewRecordingPage(page, vc.permission, user, vc.secretKey)
}

func (vc *client) GetCallRecordings(ctx context.Context, user *config.User, callSid string, data url.Values) (*RecordingPage, error) {
	page, err := vc.client.Calls.GetRecordings(ctx, callSid, data)
	if err != nil {
		return nil, err
	}
	return NewRecordingPage(page, vc.permission, user, vc.secretKey)
}

func (vc *client) GetCallAlerts(ctx context.Context, user *config.User, callSid string) (*AlertPage, error) {
	data := url.Values{}
	data.Set("ResourceSid", callSid)
	data.Set("PageSize", "400")
	page, err := vc.client.Monitor.Alerts.GetPage(ctx, data)
	if err != nil {
		return nil, err
	}
	return NewAlertPage(page, vc.permission, user)
}

func (vc *client) CacheCommonQueries(pageSize uint, doneCh <-chan bool) {
	timeout := time.After(1 * time.Millisecond)
	ps := strconv.FormatUint(uint64(pageSize), 10)
	data := url.Values{"PageSize": []string{ps}}
	// we could add timeouts here but not much value; these all happen in the
	// background and the twilio client sets a 31 second timeout on all
	// requests.
	ctx := context.Background()
	for {
		select {
		case <-timeout:
			go vc.getAndCacheMessage(ctx, twilio.Epoch, twilio.HeatDeath, data)
			go vc.getAndCacheCall(ctx, twilio.Epoch, twilio.HeatDeath, data)
			go vc.getAndCacheConference(ctx, twilio.Epoch, twilio.HeatDeath, data)
			go vc.getAndCacheAlert(ctx, twilio.Epoch, twilio.HeatDeath, data)
			go vc.getNumbers()
		case <-doneCh:
			return
		}
		timeout = time.After(30 * time.Second)
	}
}

func (vc *client) IsTwilioNumber(num twilio.PhoneNumber) bool {
	vc.numbersMu.RLock()
	_, ok := vc.numbers[num]
	vc.numbersMu.RUnlock()
	return ok
}
