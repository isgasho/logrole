package views

import (
	"errors"

	types "github.com/kevinburke/go-types"
	twilio "github.com/kevinburke/twilio-go"
	"github.com/kevinburke/logrole/config"
)

type Message struct {
	user    *config.User
	message *twilio.Message
}

type MessagePage struct {
	messages        []*Message
	previousPageURI types.NullString
	nextPageURI     types.NullString
}

func (mp *MessagePage) Messages() []*Message {
	return mp.messages
}

func (mp *MessagePage) NextPageURI() types.NullString {
	return mp.nextPageURI
}

func (mp *MessagePage) PreviousPageURI() types.NullString {
	return mp.previousPageURI
}

const showAllColumnsOnEmptyPage = true

// ShowHeader returns true if we should show the table header in the message
// list view. This is true if the user is allowed to view the fieldName on any
// message in the list, and true if there are no messages.
func (mp *MessagePage) ShowHeader(fieldName string) bool {
	if mp == nil {
		return showAllColumnsOnEmptyPage
	}
	msgs := mp.Messages()
	if len(msgs) == 0 {
		return showAllColumnsOnEmptyPage
	}
	for _, message := range msgs {
		if message.CanViewProperty(fieldName) {
			return true
		}
	}
	return false
}

func NewMessagePage(mp *twilio.MessagePage, p *config.Permission, u *config.User) (*MessagePage, error) {
	if u.CanViewMessages() == false {
		return nil, config.PermissionDenied
	}
	messages := make([]*Message, 0)
	for _, message := range mp.Messages {
		msg, err := NewMessage(message, p, u)
		if err == config.ErrTooOld || err == config.PermissionDenied {
			continue
		}
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	var npuri types.NullString
	if len(messages) > 0 {
		npuri = mp.NextPageURI
	}
	return &MessagePage{
		messages:        messages,
		nextPageURI:     npuri,
		previousPageURI: mp.PreviousPageURI,
	}, nil
}

// CanViewProperty returns true if the caller can access the given property.
// CanViewProperty panics if the property does not exist. The input is
// case-sensitive; "MessagingServiceSid" is the correct casing.
func (m *Message) CanViewProperty(property string) bool {
	if m.user == nil {
		return false
	}
	switch property {
	case "Sid", "DateCreated", "DateUpdated", "MessagingServiceSid",
		"Status", "Direction", "ErrorCode",
		"ErrorMessage":
		return m.user.CanViewMessages()
	case "Price", "PriceUnit":
		return m.user.CanViewMessagePrice()
	case "NumMedia":
		return m.user.CanViewNumMedia()
	case "From":
		return m.user.CanViewMessageFrom()
	case "To":
		return m.user.CanViewMessageTo()
	case "Body", "NumSegments":
		return m.user.CanViewMessageBody()
	default:
		panic("unknown property " + property)
	}
}

func (m *Message) NumMedia() (twilio.NumMedia, error) {
	if m.user.CanViewNumMedia() {
		return m.message.NumMedia, nil
	} else {
		return 0, config.PermissionDenied
	}
}

func (m *Message) Sid() (string, error) {
	if m.CanViewProperty("Sid") {
		return m.message.Sid, nil
	} else {
		return "", config.PermissionDenied
	}
}

func (m *Message) DateCreated() (twilio.TwilioTime, error) {
	if m.CanViewProperty("DateCreated") {
		return m.message.DateCreated, nil
	} else {
		return twilio.TwilioTime{}, config.PermissionDenied
	}
}

func (m *Message) From() (twilio.PhoneNumber, error) {
	if m.CanViewProperty("From") {
		return m.message.From, nil
	} else {
		return twilio.PhoneNumber(""), config.PermissionDenied
	}
}

func (m *Message) To() (twilio.PhoneNumber, error) {
	if m.CanViewProperty("To") {
		return m.message.To, nil
	} else {
		return twilio.PhoneNumber(""), config.PermissionDenied
	}
}

func (m *Message) MessagingServiceSid() (types.NullString, error) {
	if m.CanViewProperty("MessagingServiceSid") {
		return m.message.MessagingServiceSid, nil
	} else {
		return types.NullString{}, config.PermissionDenied
	}
}

func (m *Message) Status() (twilio.Status, error) {
	if m.CanViewProperty("Status") {
		return m.message.Status, nil
	} else {
		return twilio.Status(""), config.PermissionDenied
	}
}

func (m *Message) Direction() (twilio.Direction, error) {
	if m.CanViewProperty("Direction") {
		return m.message.Direction, nil
	} else {
		return twilio.Direction(""), config.PermissionDenied
	}
}

func (m *Message) Price() (string, error) {
	if m.CanViewProperty("Price") {
		return m.message.Price, nil
	} else {
		return "", config.PermissionDenied
	}
}

func (m *Message) PriceUnit() (string, error) {
	if m.CanViewProperty("PriceUnit") {
		return m.message.PriceUnit, nil
	} else {
		return "", config.PermissionDenied
	}
}

func (m *Message) FriendlyPrice() (string, error) {
	if m.CanViewProperty("Price") && m.CanViewProperty("PriceUnit") {
		return m.message.FriendlyPrice(), nil
	} else {
		return "", config.PermissionDenied
	}
}

func (m *Message) Body() (string, error) {
	if m.CanViewProperty("Body") {
		return m.message.Body, nil
	} else {
		return "", config.PermissionDenied
	}
}

func (m *Message) NumSegments() (twilio.Segments, error) {
	if m.CanViewProperty("NumSegments") {
		return m.message.NumSegments, nil
	} else {
		return 0, config.PermissionDenied
	}
}

func (m *Message) ErrorCode() (twilio.Code, error) {
	if m.CanViewProperty("ErrorCode") {
		return m.message.ErrorCode, nil
	} else {
		return 0, config.PermissionDenied
	}
}

func (m *Message) ErrorMessage() (string, error) {
	if m.CanViewProperty("ErrorMessage") {
		return m.message.ErrorMessage, nil
	} else {
		return "", config.PermissionDenied
	}
}

func (m *Message) CanViewMedia() bool {
	// Hack - a separate function since this is not a property on the object.
	return m.user != nil && m.user.CanViewMedia()
}

// NewMessage creates a new Message, setting fields to be hidden or shown as
// appropriate for the given Permission and User.
func NewMessage(msg *twilio.Message, p *config.Permission, u *config.User) (*Message, error) {
	if u.CanViewMessages() == false {
		return nil, config.PermissionDenied
	}
	if msg.DateCreated.Valid == false {
		return nil, errors.New("Invalid DateCreated for message")
	}
	if !u.CanViewResource(msg.DateCreated.Time, p.MaxResourceAge()) {
		return nil, config.ErrTooOld
	}
	return &Message{user: u, message: msg}, nil
}
