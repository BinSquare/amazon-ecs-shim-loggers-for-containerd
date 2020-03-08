// +build unit

package logger

import (
	"fmt"
	"io/ioutil"
	"os"
	"sync"
	"testing"
	"time"

	mock_logger "github.com/aws/shim-loggers-for-containerd/logger/mocks"

	dockerlogger "github.com/docker/docker/daemon/logger"
	"github.com/golang/mock/gomock"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

const (
	maxRetries        = 3
	testErrMsg        = "test error message"
	testContainerID   = "test-container-id"
	testContainerName = "test-container-name"
)

var (
	dummyLogMsg            = []byte("test log message")
	dummySource            = "stdout"
	dummyTime              = time.Date(2020, time.January, 14, 01, 59, 0, 0, time.UTC)
	logDestinationFileName string
)

// dummyClient is only used for testing. It owns the customized Log function used in
// TestSendLogs test case as we do not need the functionality that the actual Log function
// is doing inside the test. Mock Log function is not enough here as there does not exist a
// better way to verify what happened in the TestSendLogs test, which has a goroutine.
type dummyClient struct{}

// Log implements customized workflow used for testing purpose.
// This is only trigger in TestSendLogs test case. It writes current log message to the end of
// tmp test file, which makes sure the function itself accepts and "logging" the message
// correctly.
func (d *dummyClient) Log(msg *dockerlogger.Message) error {
	_, err := os.Stat(logDestinationFileName)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(logDestinationFileName, os.O_APPEND|os.O_RDWR, 0644)
	if err != nil {
		return errors.Wrapf(err,
			"unable to open file %s to record log message", logDestinationFileName)
	}
	defer f.Close()
	f.Write(msg.Line)

	return nil
}

// TestLogWithRetry tests function LogWithRetry does not retry on success or retries
// on error.
func TestLogWithRetry(t *testing.T) {
	t.Run("DoesNotRetry", testLogWithRetryDoesNotRetry)
	t.Run("WithError", testLogWithRetryWithError)
}

// testLogWithRetryDoesNotRetry tests LogWithRetry function did not retry on no error
// returned from Log function.
func testLogWithRetryDoesNotRetry(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockStream := mock_logger.NewMockclient(ctrl)
	l := &Logger{
		Info:   &dockerlogger.Info{},
		Stream: mockStream,
	}
	mockStream.EXPECT().Log(gomock.Any()).Return(nil).Times(1)
	err := l.LogWithRetry(dummyLogMsg, dummySource, dummyTime)
	require.NoError(t, err)
}

// testLogWithRetryWithError tests LogWithRetry function retries on error returned from
// Log function.
func testLogWithRetryWithError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockStream := mock_logger.NewMockclient(ctrl)
	l := &Logger{
		Info:   &dockerlogger.Info{},
		Stream: mockStream,
	}
	mockStream.EXPECT().Log(gomock.Any()).Return(errors.New(testErrMsg)).Times(maxRetries)
	expectErrMsg := fmt.Sprintf("sending container logs to destination has been retried for %d times: %s",
		maxRetries, testErrMsg)
	err := l.LogWithRetry(dummyLogMsg, dummySource, dummyTime)
	require.Error(t, err)
	require.Contains(t, expectErrMsg, err.Error())
}

// TestSendLogs tests sendLogs goroutine that gets log message from mock io pipe and sends
// to mock destination. In this test case, the source and destination are both tmp files that
// read from and write to inside the customized Log function.
func TestSendLogs(t *testing.T) {
	l := &Logger{
		Info:   &dockerlogger.Info{},
		Stream: &dummyClient{},
	}
	// Create a tmp file that used to mock the io pipe where the logger reads log
	// messages from.
	tmpIOSource, err := ioutil.TempFile("", "")
	require.NoError(t, err)
	defer os.Remove(tmpIOSource.Name())
	var expectedSize int64
	lines := []string{
		"First line to write",
		"Second line to write",
	}
	for _, line := range lines {
		expectedSize += int64(len(line))
		tmpIOSource.WriteString(line)
	}

	// Create a tmp file that used to inside customized dummy Log function where the
	// logger sends log messages to.
	tmpDest, err := ioutil.TempFile(os.TempDir(), "")
	require.NoError(t, err)
	defer os.Remove(tmpDest.Name())
	logDestinationFileName = tmpDest.Name()

	var wg sync.WaitGroup
	wg.Add(1)
	f, err := os.Open(tmpIOSource.Name())
	require.NoError(t, err)
	defer f.Close()
	go l.sendLogs(f, &wg, dummySource, -1, -1)
	wg.Wait()

	// Make sure the new scanned log message has been written to the tmp file by sendLogs
	// goroutine.
	logDestinationInfo, err := os.Stat(logDestinationFileName)
	require.NoError(t, err)
	require.Equal(t, expectedSize, logDestinationInfo.Size())
}

// TestNewInfo tests if NewInfo function creates logger info correctly.
func TestNewInfo(t *testing.T) {
	config := map[string]string{
		"configKey1": "configVal1",
		"configKey2": "configVal2",
		"configKey3": "configVal3",
	}
	info := NewInfo(testContainerID, testContainerName, WithConfig(config))
	require.Equal(t, config, info.Config)
}