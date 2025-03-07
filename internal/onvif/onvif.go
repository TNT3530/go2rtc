package onvif

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/AlexxIT/go2rtc/internal/api"
	"github.com/AlexxIT/go2rtc/internal/app"
	"github.com/AlexxIT/go2rtc/internal/rtsp"
	"github.com/AlexxIT/go2rtc/internal/streams"
	"github.com/AlexxIT/go2rtc/pkg/core"
	"github.com/AlexxIT/go2rtc/pkg/onvif"
	"github.com/rs/zerolog"
)

type Stream struct {
	StreamWidth string `yaml:"width"`
	StreamHeight string `yaml:"height"`
	StreamFramerate string `yaml:"framerate"`
	StreamBitrate string `yaml:"bitrate"`
}

var conf struct {
	Mod struct {
		Streams map[string]Stream `yaml:"streams"`
		DeviceName string `yaml:"deviceName"`
		DeviceSerial string `yaml:"deviceSerial"`
		DeviceMaxHeight string `yaml:"deviceMaxHeight"`
		DeviceMaxWidth string `yaml:"deviceMaxWidth"`
		DeviceMaxFramerate string `yaml:"deviceMaxFramerate"`
	} `yaml:"onvif"`
	StreamNames []string
}
	
func Init() {
	conf.Mod.DeviceName = "go2rtc_default"
	conf.Mod.DeviceSerial = "00000000"

	app.LoadConfig(&conf)
	app.Info["onvif"] = conf.Mod

	log = app.GetLogger("onvif")
	
	conf.StreamNames = make([]string, len(conf.Mod.Streams))
	var i int = 0
	for name, _ := range conf.Mod.Streams {
		//fmt.Printf("%+v\n", item)
		conf.StreamNames[i] = name
		i++
	}

	streams.HandleFunc("onvif", streamOnvif)

	// ONVIF server on all suburls
	api.HandleFunc("/onvif/", onvifDeviceService)

	// ONVIF client autodiscovery
	api.HandleFunc("api/onvif", apiOnvif)
}

var log zerolog.Logger

func streamOnvif(rawURL string) (core.Producer, error) {
	client, err := onvif.NewClient(rawURL)
	if err != nil {
		return nil, err
	}

	uri, err := client.GetURI()
	if err != nil {
		return nil, err
	}

	log.Debug().Msgf("[onvif] new uri=%s", uri)

	return streams.GetProducer(uri)
}

func onvifDeviceService(w http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	operation := onvif.GetRequestAction(b)
	if operation == "" {
		http.Error(w, "malformed request body", http.StatusBadRequest)
		return
	}

	log.Trace().Msgf("[onvif] server request %s %s:\n%s", r.Method, r.RequestURI, b)

	switch operation {
	case onvif.DeviceGetNetworkInterfaces, // important for Hass
		onvif.DeviceGetSystemDateAndTime, // important for Hass
		onvif.DeviceGetDiscoveryMode,
		onvif.DeviceGetDNS,
		onvif.DeviceGetHostname,
		onvif.DeviceGetNetworkDefaultGateway,
		onvif.DeviceGetNetworkProtocols,
		onvif.DeviceGetNTP,
		onvif.DeviceGetScopes:
		b = onvif.StaticResponse(operation)

	case onvif.DeviceGetCapabilities:
		// important for Hass: Media section
		b = onvif.GetCapabilitiesResponse(r.Host)

	case onvif.DeviceGetServices:
		b = onvif.GetServicesResponse(r.Host)

	case onvif.DeviceGetDeviceInformation:
		// important for Hass: SerialNumber (unique server ID)
		//manuf, model, firmware, serial string
		b = onvif.GetDeviceInformationResponse("", conf.Mod.DeviceName, app.Version, conf.Mod.DeviceSerial)

	case onvif.ServiceGetServiceCapabilities:
		// important for Hass
		// TODO: check path links to media
		b = onvif.GetMediaServiceCapabilitiesResponse()

	case onvif.DeviceSystemReboot:
		b = onvif.StaticResponse(operation)

		time.AfterFunc(time.Second, func() {
			os.Exit(0)
		})

	case onvif.MediaGetVideoSources:
		//b = onvif.GetVideoSourcesResponse(streams.GetAllNames())
		b = GetVideoSourcesResponse_c()

	case onvif.MediaGetProfiles:
		// important for Hass: H264 codec, width, height
		//b = onvif.GetProfilesResponse(streams.GetAllNames())
		b = GetProfilesResponse_c(conf.StreamNames)

	case onvif.MediaGetProfile:
		token := onvif.FindTagValue(b, "ProfileToken")
		b = onvif.GetProfileResponse(token)

	case onvif.MediaGetVideoSourceConfiguration:
		token := onvif.FindTagValue(b, "ConfigurationToken")
		b = onvif.GetVideoSourceConfigurationResponse(token)

	case onvif.MediaGetStreamUri:
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		uri := "rtsp://" + host + ":" + rtsp.Port + "/" + onvif.FindTagValue(b, "ProfileToken")
		b = onvif.GetStreamUriResponse(uri)

	case onvif.MediaGetSnapshotUri:
		uri := "http://" + r.Host + "/api/frame.jpeg?src=" + onvif.FindTagValue(b, "ProfileToken")
		b = onvif.GetSnapshotUriResponse(uri)

	default:
		http.Error(w, "unsupported operation", http.StatusBadRequest)
		log.Debug().Msgf("[onvif] unsupported request:\n%s", b)
		return
	}

	log.Trace().Msgf("[onvif] server response:\n%s", b)

	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	if _, err = w.Write(b); err != nil {
		log.Error().Err(err).Caller().Send()
	}
}

func GetProfilesResponse_c(names []string) []byte {
	e := onvif.NewEnvelope()
	e.Append(`<trt:GetProfilesResponse>
`)
	for _, name := range names {
		appendProfile_c(e, "Profiles", name)
	}
	e.Append(`</trt:GetProfilesResponse>`)
	return e.Bytes()
}

func appendProfile_c(e *onvif.Envelope, tag, name string) {
	// empty `RateControl` important for UniFi Protect
	e.Append(`<trt:`, tag, ` token="`, name, `" fixed="true">
	<tt:Name>`, name, `</tt:Name>
	<tt:VideoSourceConfiguration token="`, conf.Mod.DeviceName, `">
		<tt:Name>VSC</tt:Name>
		<tt:SourceToken>`, conf.Mod.DeviceName, `</tt:SourceToken>
		<tt:Bounds x="0" y="0" width="`, conf.Mod.Streams[name].StreamWidth, `" height="`, conf.Mod.Streams[name].StreamHeight, `"></tt:Bounds>
	</tt:VideoSourceConfiguration>
	<tt:VideoEncoderConfiguration token="`, name, `">
		<tt:Name>VEC</tt:Name>
		<tt:Encoding>H264</tt:Encoding>
		<tt:Resolution><tt:Width>`, conf.Mod.Streams[name].StreamWidth, `</tt:Width><tt:Height>`, conf.Mod.Streams[name].StreamHeight, `</tt:Height></tt:Resolution>
		<tt:RateControl>
			<tt:FrameRateLimit>`, conf.Mod.Streams[name].StreamFramerate, `</tt:FrameRateLimit>
			<tt:BitrateLimit>`, conf.Mod.Streams[name].StreamBitrate, `</tt:BitrateLimit>
		</tt:RateControl>
	</tt:VideoEncoderConfiguration>
</trt:`, tag, `>
`)
}

func GetVideoSourcesResponse_c() []byte {
	e := onvif.NewEnvelope()
	e.Append(`<trt:GetVideoSourcesResponse>
`)
	e.Append(`<trt:VideoSources token="`, conf.Mod.DeviceName, `">
	<tt:Framerate>`, conf.Mod.DeviceMaxFramerate, `</tt:Framerate>
	<tt:Resolution><tt:Width>`, conf.Mod.DeviceMaxWidth, `</tt:Width><tt:Height>`, conf.Mod.DeviceMaxHeight, `</tt:Height></tt:Resolution>
</trt:VideoSources>
`)
	e.Append(`</trt:GetVideoSourcesResponse>`)
	return e.Bytes()
}

func apiOnvif(w http.ResponseWriter, r *http.Request) {
	src := r.URL.Query().Get("src")

	var items []*api.Source

	if src == "" {
		urls, err := onvif.DiscoveryStreamingURLs()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for _, rawURL := range urls {
			u, err := url.Parse(rawURL)
			if err != nil {
				log.Warn().Str("url", rawURL).Msg("[onvif] broken")
				continue
			}

			if u.Scheme != "http" {
				log.Warn().Str("url", rawURL).Msg("[onvif] unsupported")
				continue
			}

			u.Scheme = "onvif"
			u.User = url.UserPassword("user", "pass")

			if u.Path == onvif.PathDevice {
				u.Path = ""
			}

			items = append(items, &api.Source{Name: u.Host, URL: u.String()})
		}
	} else {
		client, err := onvif.NewClient(src)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if l := log.Trace(); l.Enabled() {
			b, _ := client.MediaRequest(onvif.MediaGetProfiles)
			l.Msgf("[onvif] src=%s profiles:\n%s", src, b)
		}

		name, err := client.GetName()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		tokens, err := client.GetProfilesTokens()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		for i, token := range tokens {
			items = append(items, &api.Source{
				Name: name + " stream" + strconv.Itoa(i),
				URL:  src + "?subtype=" + token,
			})
		}

		if len(tokens) > 0 && client.HasSnapshots() {
			items = append(items, &api.Source{
				Name: name + " snapshot",
				URL:  src + "?subtype=" + tokens[0] + "&snapshot",
			})
		}
	}

	api.ResponseSources(w, items)
}
