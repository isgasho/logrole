package views_test

import (
	"context"
	"fmt"
	"time"

	"github.com/kevinburke/handlers"
	"github.com/saintpete/logrole/config"
	"github.com/saintpete/logrole/services"
	"github.com/saintpete/logrole/views"
	twilio "github.com/kevinburke/twilio-go"
)

func Example() {
	c := twilio.NewClient("AC123", "123", nil)
	key := services.NewRandomKey()
	permission := config.NewPermission(24 * time.Hour)
	user := config.NewUser(config.AllUserSettings())

	vc := views.NewClient(handlers.Logger, c, key, permission)

	message, _ := vc.GetMessage(context.TODO(), user, "SM123")
	sid, _ := message.Sid()
	fmt.Println(sid)
}
