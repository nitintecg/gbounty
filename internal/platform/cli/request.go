package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	scan "github.com/bountysecurity/gbounty/internal"
	"github.com/bountysecurity/gbounty/internal/request"
	"github.com/bountysecurity/gbounty/kit/logger"
	"github.com/bountysecurity/gbounty/kit/url"
)

var (
	// ErrProcessRequestFile is the error returned when [Config] points to a file
	// with requests, and it could not be processed successfully.
	ErrProcessRequestFile = errors.New("could not process request(s) file")

	// ErrProcessUrlsFile is the error returned when [Config] points to a file
	// with urls, and it could not be processed successfully.
	ErrProcessUrlsFile = errors.New("could not process url(s) file")

	// ErrInvalidHeader is the error returned when [Config] contains some headers
	// configured by they have an invalid format.
	ErrInvalidHeader = errors.New("invalid header")
)

// PrepareTemplates takes a [Config] and a [scan.FileSystem], and uses the first one to
// initialize the [Template] instances that compound the scan defined by that configuration,
// and stores them into the given file system, so it is ready for the scan to start.
func PrepareTemplates(ctx context.Context, fs scan.FileSystem, cfg Config) error {
	pCfg := scan.ParamsCfg{}
	if len(cfg.ParamsFile) > 0 {
		params, err := readParamsFile(ctx, cfg.ParamsFile)
		switch err {
		case nil:
			pCfg.Params = params
			pCfg.Size = cfg.ParamsSplit
			pCfg.Method = strings.ToUpper(cfg.ParamsMethod)
			pCfg.Encoding = strings.ToLower(cfg.ParamsEncoding)
		default:
			logger.For(ctx).Errorf("Error while reading params file: %s", err.Error())
		}
	}
	return createTemplates(ctx, fs, cfg, pCfg)
}

func readParamsFile(ctx context.Context, pathToFile string) ([]string, error) {
	logger.For(ctx).Infof("Reading params file from: %s", pathToFile)

	contents, err := os.ReadFile(pathToFile)
	if err != nil {
		return nil, err
	}

	var params []string
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	for scanner.Scan() {
		params = append(params, strings.TrimSpace(scanner.Text()))
	}

	return params, nil
}

func createTemplates(ctx context.Context, fs scan.FileSystem, cfg Config, pCfg scan.ParamsCfg) error {
	logger.For(ctx).Info("Preparing templates for scan")

	if len(cfg.RequestsFile) > 0 {
		logger.For(ctx).Infof("Scan templates from requests file: %s", cfg.RequestsFile)
		return createFromRequestsFile(ctx, fs, cfg.RequestsFile, pCfg)
	}

	if len(cfg.RawRequests) > 0 {
		logger.For(ctx).Infof("Scan templates from raw requests: %s", cfg.RawRequests)
		return createFromRawRequestFiles(ctx, fs, cfg.RawRequests, pCfg)
	}

	if len(cfg.UrlsFile) > 0 {
		logger.For(ctx).Info("Updating config with urls file")

		err := updateConfigWithURLS(ctx, &cfg)
		if err != nil {
			logger.For(ctx).Errorf("Error while updating config with urls file: %s", err.Error())
			return err
		}
	}

	logger.For(ctx).Infof("Scan templates from config")
	return createFromConfig(ctx, fs, cfg, pCfg)
}

func createFromRequestsFile(ctx context.Context, fs scan.FileSystem, path string, pCfg scan.ParamsCfg) error {
	file, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w(%s): %s", ErrProcessRequestFile, path, err.Error())
	}

	templates, err := scan.TemplatesFromZipBytes(ctx, pCfg, file)
	if err != nil {
		return fmt.Errorf("%w(%s): %s", ErrProcessRequestFile, path, err.Error())
	}

	for _, template := range templates {
		err := fs.StoreTemplate(ctx, template)
		if err != nil {
			return fmt.Errorf("%w(%s) %s", ErrProcessRequestFile, path, err.Error())
		}
	}

	return nil
}

func createFromRawRequestFiles(ctx context.Context, fs scan.FileSystem, paths MultiValue, pCfg scan.ParamsCfg) error {
	var tplIdx int
	for _, path := range paths {
		bytes, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("%w(%s): %s", ErrProcessRequestFile, path, err.Error())
		}

		templates, err := scan.TemplateFromRawBytes(ctx, tplIdx, pCfg, bytes)
		if err != nil {
			return fmt.Errorf("%w(%s): %s", ErrProcessRequestFile, path, err.Error())
		}

		for _, tpl := range templates {
			tplIdx++
			err = fs.StoreTemplate(ctx, tpl)
			if err != nil {
				return fmt.Errorf("%w(%s): %s", ErrProcessRequestFile, path, err.Error())
			}
		}
	}

	return nil
}

func createFromConfig(ctx context.Context, fs scan.FileSystem, cfg Config, pCfg scan.ParamsCfg) error {
	var options []request.Option

	if len(cfg.Method) > 0 {
		logger.For(ctx).Infof("HTTP method inherited from config: %s", cfg.Method)

		options = append(options, request.WithMethod(cfg.Method))
	}

	if len(cfg.Data) > 0 {
		logger.For(ctx).Infof("Payload data inherited from config: %s", cfg.Data.String())

		options = append(options, request.WithData([]byte(strings.Join(cfg.Data, "&"))))
	}

	if len(cfg.Headers) > 0 {
		logger.For(ctx).Infof("HTTP headers inherited from config: %s", cfg.Headers.String())

		for _, header := range cfg.Headers {
			if len(strings.SplitN(header, ":", 2)) != 2 {
				return fmt.Errorf("%w: %s", ErrInvalidHeader, header)
			}

			key := strings.TrimSpace(strings.SplitN(header, ":", 2)[0])
			value := strings.TrimSpace(strings.SplitN(header, ":", 2)[1])
			options = append(options, request.WithHeader(key, value))
		}
	}

	var tplIdx int
	for _, cfgURL := range cfg.URLS {
		err := url.Validate(&cfgURL) //nolint:gosec,scopelint
		if err != nil {
			logger.For(ctx).Errorf("Error while validating url (%s): %s", cfgURL, err.Error())

			return err
		}

		reqWithOpts := request.WithOptions(cfgURL, options...)
		templates := pCfg.Alter(scan.NewTemplate(ctx, tplIdx, reqWithOpts, nil))

		for _, tpl := range templates {
			tplIdx++
			err = fs.StoreTemplate(ctx, tpl)
			if err != nil {
				logger.For(ctx).Errorf("Error while building scan template: %s", err.Error())

				return fmt.Errorf("%w(%s): %s", ErrProcessRequestFile, cfgURL, err.Error())
			}
		}
	}

	return nil
}

func updateConfigWithURLS(ctx context.Context, cfg *Config) error {
	file, err := os.Open(cfg.UrlsFile)
	if err != nil {
		var pathErr *os.PathError

		if errors.As(err, &pathErr) {
			return fmt.Errorf("%w(%s): %s", ErrProcessUrlsFile, cfg.UrlsFile, pathErr.Err) //nolint:errorlint
		}

		return fmt.Errorf("%w(%s): %s", ErrProcessUrlsFile, cfg.OutPath, err) //nolint:errorlint
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		err := url.Validate(&line)
		if err != nil {
			logger.For(ctx).Warnf("Skipping url(s) file (%s) line (%s) - not a valid url: %s", cfg.UrlsFile, line, err.Error())
			continue
		}

		cfg.URLS = append(cfg.URLS, line)
	}

	return scanner.Err()
}