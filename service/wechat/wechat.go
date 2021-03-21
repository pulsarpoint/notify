package wechat


import (
    "context"
    "fmt"
    "github.com/pkg/errors"
    "github.com/silenceper/wechat/v2"
    "github.com/silenceper/wechat/v2/cache"
    "github.com/silenceper/wechat/v2/officialaccount/config"
    "github.com/silenceper/wechat/v2/officialaccount/message"
    "github.com/silenceper/wechat/v2/util"
    "net/http"
    "sync"
)


// Config is the Service configuration.
type Config struct {
    AppID 			string
    AppSecret 		string
    Token			string
    EncodingAESKey 	string
}

// wechatMessageManager abstracts go-wechat's message.Manager for writing unit tests
type wechatMessageManager interface {
    Send(msg *message.CustomerMessage) error
}

// Service encapsulates the WeChat client along with internal state for storing users.
type Service struct {
    config 			Config
    messageManager	wechatMessageManager
    userIDs 		[]string
}

// New returns a new instance of a WeChat notification service.
func New(cfg Config) *Service {

    wc := wechat.NewWechat()
    wcCfg := &config.Config{
        AppID:     		cfg.AppID,
        AppSecret: 		cfg.AppSecret,
        Token:     		cfg.Token,
        EncodingAESKey: cfg.EncodingAESKey,
        Cache: 			cache.NewMemory(),
    }

    oa := wc.GetOfficialAccount(wcCfg)

    return &Service{
        config: cfg,
        messageManager: oa.GetCustomerMessageManager(),
    }
}

// WaitForOneOffVerification waits for the verification call from the WeChat backend.
//
// Should be running when (re-)applying settings in wechat configuration.
//
// Set devMode to true when using the sandbox.
//
// See https://developers.weixin.qq.com/doc/offiaccount/en/Basic_Information/Access_Overview.html
func (s *Service) WaitForOneOffVerification(
    addr string,
    devMode bool,
    callback func(r *http.Request, verified bool)) error {

    srv := &http.Server{Addr: addr}
    verificationDone := &sync.WaitGroup{}
    verificationDone.Add(1)

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        query := r.URL.Query()

        echoStr := query.Get("echostr")
        if devMode {
            if callback != nil {
                callback(r, true)
            }
            w.Write([]byte(echoStr))
            // verification done; dev mode
            verificationDone.Done()
            return
        } else {
            // perform signature check
            timestamp := query.Get("timestamp")
            nonce := query.Get("nonce")
            suppliedSignature := query.Get("signature")
            computedSignature := util.Signature(s.config.Token, timestamp, nonce)
            if suppliedSignature == computedSignature {
                if callback != nil {
                    callback(r, true)
                }
                w.Write([]byte(echoStr))
                // verification done; prod mode
                verificationDone.Done()
                return
            }
        }
        // verification not done (keep waiting)
        if callback != nil {
            callback(r, false)
        }
    })

    var err error
    go func() {
        if innerErr := srv.ListenAndServe(); innerErr != http.ErrServerClosed {
            err = errors.Wrapf(innerErr, "failed to wait for verification at '%s'", addr)
        }
    }()

    // wait until verification is done and shutdown the server
    verificationDone.Wait()

    srv.Shutdown(context.TODO())

    return err
}

// AddReceivers takes user ids and adds them to the internal users list. The Send method will send
// a given message to all those users.
func (s *Service) AddReceivers(userIDs ...string) {
    s.userIDs = append(s.userIDs, userIDs...)
}

// Send takes a message subject and a message content and sends them to all previously set users.
func (s *Service) Send(ctx context.Context, subject, content string) error {

    for _, userID := range s.userIDs {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
            text := fmt.Sprintf("%s\n%s", subject, content)
            if err := s.messageManager.Send(message.NewCustomerTextMessage(userID, text)); err != nil {
                return errors.Wrapf(err, "failed to send message to WeChat user '%s'", userID)
            }
        }
    }

    return nil
}

