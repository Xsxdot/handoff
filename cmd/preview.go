package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

var (
	previewPort int
	previewPath string
	previewVia  []string
)

var previewCmd = &cobra.Command{
	Use:   "preview",
	Short: "发布并管理远端预览会话",
}

var previewOpenCmd = &cobra.Command{
	Use:   "open",
	Short: "发布一个远端预览会话",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cl, cleanup, err := newTargetClient()
		if err != nil {
			return err
		}
		defer cleanup()
		session, err := cl.CreatePreview(cmd.Context(), proto.PreviewOpenReq{
			Port: previewPort, Path: previewPath, Via: previewVia,
		})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "预览已发到桌面，在对应项目任务组点开（%s）\n", session.ID)
		return err
	},
}

var previewListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出预览会话",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cl, cleanup, err := newTargetClient()
		if err != nil {
			return err
		}
		defer cleanup()
		resp, err := cl.ListPreviews(cmd.Context())
		if err != nil {
			return err
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		for i := range resp.Sessions {
			if err := enc.Encode(&resp.Sessions[i]); err != nil {
				return err
			}
		}
		return nil
	},
}

var previewCloseCmd = &cobra.Command{
	Use:   "close <id>",
	Short: "关闭一个预览会话",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cl, cleanup, err := newTargetClient()
		if err != nil {
			return err
		}
		defer cleanup()
		_, err = cl.ClosePreview(cmd.Context(), args[0])
		return err
	},
}

func init() {
	previewOpenCmd.Flags().IntVar(&previewPort, "port", 0, "执行机 loopback 服务端口")
	previewOpenCmd.Flags().StringVar(&previewPath, "path", "", "执行机工作区内相对路径")
	previewOpenCmd.Flags().StringSliceVar(&previewVia, "via", nil, "额外允许投影的 IP/CIDR/域名列表")
	previewCmd.AddCommand(previewOpenCmd, previewListCmd, previewCloseCmd)
	rootCmd.AddCommand(previewCmd)
}
