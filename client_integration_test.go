// +build integration

package disgord

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

var token = os.Getenv("DISGORD_TOKEN_INTEGRATION_TEST")

var guildTypical = struct {
	ID                  Snowflake
	VoiceChannelGeneral Snowflake
	VoiceChannelOther1  Snowflake
	VoiceChannelOther2  Snowflake
}{
	ID:                  486833611564253184,
	VoiceChannelGeneral: 486833611564253188,
	VoiceChannelOther1:  673893473409171477,
	VoiceChannelOther2:  673893496356339724,
}

func TestClient(t *testing.T) {
	wg := &sync.WaitGroup{}

	status := &UpdateStatusPayload{
		Status: StatusIdle,
		Game: &Activity{
			Name: "hello",
		},
	}

	var c *Client
	wg.Add(1)
	t.Run("New", func(t *testing.T) {
		defer wg.Done()
		var err error
		c, err = NewClient(Config{
			BotToken:     token,
			DisableCache: true,
			Logger:       DefaultLogger(false),
			Presence:     status,
		})
		if err != nil {
			t.Fatal("failed to initiate a client")
		}
	})
	wg.Wait()

	wg.Add(1)
	t.Run("premature-emit", func(t *testing.T) {
		defer wg.Done()
		if _, err := c.Emit(UpdateStatus, &UpdateStatusPayload{}); err == nil {
			t.Fatal("Emit should have failed as no shards have been connected (initialised)")
		}
	})
	wg.Wait()

	defer c.Disconnect()
	wg.Add(1)
	t.Run("connect", func(t *testing.T) {
		defer wg.Done()
		if err := c.Connect(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	wg.Wait()

	wg.Add(1)
	t.Run("ready", func(t *testing.T) {
		defer wg.Done()
		ready := make(chan interface{}, 2)
		c.Ready(func() {
			ready <- true
		})
		select {
		case <-time.After(10 * time.Second):
			t.Fatal("unable to connect within time frame of 10s")
		case <-ready:
		}
	})
	wg.Wait()

	wg.Add(1)
	t.Run("default-presence", func(t *testing.T) {
		defer wg.Done()
		done := make(chan bool, 2)
		c.On(EvtPresenceUpdate, func(_ Session, evt *PresenceUpdate) {
			if !evt.User.Bot {
				c.Logger().Info("was not bot")
				return
			}
			usr, err := c.GetCurrentUser(context.Background())
			if err != nil {
				done <- false
				return
			}
			if evt.User.ID != usr.ID {
				return
			}

			if evt.Status != StatusIdle {
				done <- false
				return
			}
			if evt.Game == nil {
				done <- false
				return
			}
			if evt.Game.Name != "hello" {
				done <- false
				return
			}

			done <- true
		})
		if _, err := c.Emit(UpdateStatus, status); err != nil {
			t.Fatal(err)
		}

		select {
		case <-time.After(10 * time.Second):
			// yay
			// if no presence update is fired after calling emit,
			// that means that no change took place.
			// TODO: this test is fragile
		case success := <-done:
			if success {
				t.Fatal("unable to set presence at boot")
			}
		}
	})
	wg.Wait()

	wg.Add(1)
	t.Run("voice/MoveTo", func(t *testing.T) {
		defer wg.Done()
		deadline, _ := context.WithDeadline(context.Background(), time.Now().Add(25*time.Second))

		oldChannelID := guildTypical.VoiceChannelGeneral
		newChannelID := guildTypical.VoiceChannelOther1
		connectedToVoiceChannel := make(chan bool)
		successfullyMoved := make(chan bool, 2)
		done := make(chan bool)
		defer close(successfullyMoved)

		c.On(EvtVoiceStateUpdate, func(_ Session, evt *VoiceStateUpdate) {
			myself, err := c.GetCurrentUser(context.Background())
			if err != nil {
				panic(err)
			}
			if evt.UserID != myself.ID {
				return
			}
			if evt.ChannelID == oldChannelID {
				connectedToVoiceChannel <- true
				return
			}
			if evt.ChannelID == newChannelID {
				successfullyMoved <- true
				successfullyMoved <- true
			} else {
				successfullyMoved <- false
				successfullyMoved <- false
			}
		})

		go func() {
			v, err := c.VoiceConnect(guildTypical.ID, oldChannelID)
			if err != nil {
				t.Fatal(err)
			}

			select {
			case <-connectedToVoiceChannel:
			case <-deadline.Done():
				panic("connectedToVoiceChannel did not emit")
			}
			if err = v.MoveTo(newChannelID); err != nil {
				t.Fatal(err)
			}

			select {
			case <-successfullyMoved:
			case <-deadline.Done():
				panic("successfullyMoved did not emit")
			}

			defer func() {
				close(done)
			}()
			if err = v.Close(); err != nil {
				t.Fatal(err)
			}
			<-time.After(50 * time.Millisecond)
		}()

		testFinished := sync.WaitGroup{}
		testFinished.Add(1)
		go func() {
			select {
			case <-time.After(10 * time.Second):
				t.Fatal("switching to a different voice channel failed")
			case success, ok := <-successfullyMoved:
				if !ok {
					t.Fatal("unexpected close of channel")
				}
				if !success {
					t.Fatal("did not go to a different voice channel")
				}
			}
			testFinished.Done()
		}()
		testFinished.Wait()

		select {
		case <-done:
		case <-deadline.Done():
			panic("done did not emit")
		}
	})
	wg.Wait()
}

func TestConnectWithShards(t *testing.T) {
	<-time.After(6 * time.Second) // avoid identify abuse
	c := New(Config{
		BotToken:     token,
		DisableCache: true,
		Logger:       DefaultLogger(true),
		ShardConfig: ShardConfig{
			ShardIDs: []uint{0, 1},
		},
	})
	defer c.Disconnect()
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	done := make(chan interface{}, 2)
	c.Ready(func() {
		done <- true
	})

	select {
	case <-time.After(15 * time.Second):
		t.Fatal("unable to connect within time frame of 10s")
	case <-done:
	}
}

func TestConnectWithSeveralInstances(t *testing.T) {
	<-time.After(6 * time.Second) // avoid identify abuse
	createInstance := func(shardIDs []uint, shardCount uint) *Client {
		return New(Config{
			BotToken:     token,
			DisableCache: true,
			Logger:       DefaultLogger(true),
			ShardConfig: ShardConfig{
				ShardIDs:   shardIDs,
				ShardCount: shardCount,
			},
		})
	}

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(20*time.Second))
	done := make(chan interface{}, 2)
	instanceReady := make(chan interface{}, 3)
	go func() {
		untilZero := 2
		for {
			select {
			case <-instanceReady:
				untilZero--
			case <-ctx.Done():
				return
			}

			if untilZero == 0 {
				done <- true
				return
			}
		}
	}()

	shardCount := uint(2)
	var instances []*Client
	for i := uint(0); i < shardCount; i++ {
		instance := createInstance([]uint{i}, shardCount)
		instances = append(instances, instance)

		instance.Ready(func() {
			instanceReady <- true
		})
		if err := instance.Connect(context.Background()); err != nil {
			cancel()
			t.Error(err)
			return
		}
		<-time.After(5 * time.Second)
	}

	defer func() {
		for i := range instances {
			_ = instances[i].Disconnect()
		}
	}()
	select {
	case <-ctx.Done():
		t.Error("unable to connect within time frame")
	case <-done:
	}
}
