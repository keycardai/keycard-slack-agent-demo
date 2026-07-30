package slackapp

import (
	"context"
	"log"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type Service struct {
	Client  *slack.Client
	Socket  *socketmode.Client
	Handler *Handler
}

func NewService(client *slack.Client, handler *Handler) *Service {
	socketClient := socketmode.New(client)
	return &Service{Client: client, Socket: socketClient, Handler: handler}
}

func (s *Service) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		log.Println("slack socket shutting down")
	}()

	go s.handleEvents()
	return s.Socket.Run()
}

func (s *Service) handleEvents() {
	for evt := range s.Socket.Events {
		switch evt.Type {
		case socketmode.EventTypeEventsAPI:
			eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
			if !ok {
				continue
			}
			s.Socket.Ack(*evt.Request)

			if eventsAPIEvent.Type != slackevents.CallbackEvent {
				continue
			}

			teamID := eventsAPIEvent.TeamID
			switch ev := eventsAPIEvent.InnerEvent.Data.(type) {
			case *slackevents.MessageEvent:
				if ev.SubType != "" || ev.BotID != "" || ev.User == "" {
					continue
				}
				go s.Handler.HandleMessage(teamID, ev.User, ev.Channel, ev.ChannelType, ev.Text, ev.ThreadTimeStamp, ev.TimeStamp)
			}

		case socketmode.EventTypeSlashCommand:
			cmd, ok := evt.Data.(slack.SlashCommand)
			if !ok {
				continue
			}
			s.Socket.Ack(*evt.Request)
			go s.Handler.HandleSlashCommand(cmd)
		}
	}
}
