// コンストラクタ NewVisionLogoDetector は ADC（Application Default Credentials）を
// 前提としており、実クレデンシャルなしに検証できないためテスト対象外です。
// 本テストでは bufconn 上のフェイク gRPC サーバーに向けた VisionLogoDetector を
// 構造体リテラルで直接組み立て、本番コードを変更せずに DetectLogos を検証します。
package vision

import (
	"context"
	"net"
	"testing"

	gvision "cloud.google.com/go/vision/v2/apiv1"
	visionpb "cloud.google.com/go/vision/v2/apiv1/visionpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
	rpcstatus "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/logodetection"
)

// fakeImageAnnotator は visionpb.ImageAnnotatorServer のテスト用フェイク実装です。
// BatchAnnotateImagesFunc に各テストケースの挙動を差し込みます。
type fakeImageAnnotator struct {
	visionpb.UnimplementedImageAnnotatorServer
	BatchAnnotateImagesFunc  func(context.Context, *visionpb.BatchAnnotateImagesRequest) (*visionpb.BatchAnnotateImagesResponse, error)
	BatchAnnotateImagesCalls int
}

func (f *fakeImageAnnotator) BatchAnnotateImages(ctx context.Context, req *visionpb.BatchAnnotateImagesRequest) (*visionpb.BatchAnnotateImagesResponse, error) {
	f.BatchAnnotateImagesCalls++
	return f.BatchAnnotateImagesFunc(ctx, req)
}

// newTestDetector は bufconn 上のフェイク gRPC サーバーに接続した VisionLogoDetector を生成します。
func newTestDetector(t *testing.T, fake *fakeImageAnnotator) *VisionLogoDetector {
	t.Helper()

	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)

	srv := grpc.NewServer()
	visionpb.RegisterImageAnnotatorServer(srv, fake)
	go func() {
		_ = srv.Serve(lis)
	}()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client, err := gvision.NewImageAnnotatorClient(context.Background(), option.WithGRPCConn(conn))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	return &VisionLogoDetector{client: client}
}

// TestVisionLogoDetector_DetectLogos は DetectLogos の正常系・異常系を検証します。
func TestVisionLogoDetector_DetectLogos(t *testing.T) {
	t.Parallel()

	imageBytes := []byte("dummy-image-bytes")

	t.Run("success: two logo annotations are mapped to DetectedLogo", func(t *testing.T) {
		t.Parallel()

		var gotReq *visionpb.BatchAnnotateImagesRequest
		fake := &fakeImageAnnotator{
			BatchAnnotateImagesFunc: func(_ context.Context, req *visionpb.BatchAnnotateImagesRequest) (*visionpb.BatchAnnotateImagesResponse, error) {
				gotReq = req
				return &visionpb.BatchAnnotateImagesResponse{
					Responses: []*visionpb.AnnotateImageResponse{
						{
							LogoAnnotations: []*visionpb.EntityAnnotation{
								{Description: "Company A", Score: 0.95},
								{Description: "Company B", Score: 0.80},
							},
						},
					},
				}, nil
			},
		}
		detector := newTestDetector(t, fake)

		got, err := detector.DetectLogos(context.Background(), imageBytes)

		require.NoError(t, err)
		assert.Equal(t, []logodetection.DetectedLogo{
			{Name: "Company A", Confidence: 0.95},
			{Name: "Company B", Confidence: 0.80},
		}, got)

		require.Equal(t, 1, fake.BatchAnnotateImagesCalls)
		require.Len(t, gotReq.Requests, 1)
		assert.Equal(t, imageBytes, gotReq.Requests[0].Image.Content)
		require.Len(t, gotReq.Requests[0].Features, 1)
		assert.Equal(t, visionpb.Feature_LOGO_DETECTION, gotReq.Requests[0].Features[0].Type)
	})

	t.Run("success: zero annotations returns empty slice without error", func(t *testing.T) {
		t.Parallel()

		fake := &fakeImageAnnotator{
			BatchAnnotateImagesFunc: func(_ context.Context, _ *visionpb.BatchAnnotateImagesRequest) (*visionpb.BatchAnnotateImagesResponse, error) {
				return &visionpb.BatchAnnotateImagesResponse{
					Responses: []*visionpb.AnnotateImageResponse{
						{LogoAnnotations: nil},
					},
				}, nil
			},
		}
		detector := newTestDetector(t, fake)

		got, err := detector.DetectLogos(context.Background(), imageBytes)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("success: empty Responses slice returns nil without error", func(t *testing.T) {
		t.Parallel()

		fake := &fakeImageAnnotator{
			BatchAnnotateImagesFunc: func(_ context.Context, _ *visionpb.BatchAnnotateImagesRequest) (*visionpb.BatchAnnotateImagesResponse, error) {
				return &visionpb.BatchAnnotateImagesResponse{Responses: nil}, nil
			},
		}
		detector := newTestDetector(t, fake)

		got, err := detector.DetectLogos(context.Background(), imageBytes)

		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("error: Responses[0].Error is set", func(t *testing.T) {
		t.Parallel()

		fake := &fakeImageAnnotator{
			BatchAnnotateImagesFunc: func(_ context.Context, _ *visionpb.BatchAnnotateImagesRequest) (*visionpb.BatchAnnotateImagesResponse, error) {
				return &visionpb.BatchAnnotateImagesResponse{
					Responses: []*visionpb.AnnotateImageResponse{
						{
							Error: &rpcstatus.Status{
								Code:    int32(codes.InvalidArgument),
								Message: "bad image",
							},
						},
					},
				}, nil
			},
		}
		detector := newTestDetector(t, fake)

		got, err := detector.DetectLogos(context.Background(), imageBytes)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "vision API error")
		assert.Contains(t, err.Error(), "bad image")
	})

	t.Run("error: RPC failure returns wrapped error", func(t *testing.T) {
		t.Parallel()

		// codes.Internal はクライアント側の自動リトライ対象コード（DeadlineExceeded/Unavailable）に
		// 含まれないため、決定的に1回で失敗させられる。
		fake := &fakeImageAnnotator{
			BatchAnnotateImagesFunc: func(_ context.Context, _ *visionpb.BatchAnnotateImagesRequest) (*visionpb.BatchAnnotateImagesResponse, error) {
				return nil, status.Error(codes.Internal, "boom")
			},
		}
		detector := newTestDetector(t, fake)

		got, err := detector.DetectLogos(context.Background(), imageBytes)

		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "vision API request failed")
		assert.Equal(t, 1, fake.BatchAnnotateImagesCalls)
	})
}
