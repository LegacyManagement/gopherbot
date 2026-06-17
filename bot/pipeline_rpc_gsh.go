package bot

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lnxjedi/gopherbot/robot"
	gshmod "github.com/lnxjedi/gopherbot/v2/modules/gsh"
)

type pipelineRPCGSHRunRequest struct {
	TaskPath string   `json:"task_path"`
	TaskName string   `json:"task_name"`
	WorkDir  string   `json:"work_dir,omitempty"`
	Env      []string `json:"env"`
	Args     []string `json:"args"`
}

type pipelineRPCGSHRunResponse struct {
	RetVal int    `json:"ret_val"`
	Error  string `json:"error,omitempty"`
}

type pipelineRPCGSHGetConfigRequest struct {
	TaskPath string   `json:"task_path"`
	TaskName string   `json:"task_name"`
	WorkDir  string   `json:"work_dir,omitempty"`
	Env      []string `json:"env"`
}

type pipelineRPCGSHGetConfigResponse struct {
	Config string `json:"config,omitempty"`
	Error  string `json:"error,omitempty"`
}

func runGSHExtensionViaRPC(taskPath, taskName, workDir string, env []string, privileged bool, w *worker, r robot.Robot, args []string) (robot.TaskRetVal, error) {
	resolvedWorkDir, err := resolvePipelineRPCWorkDir(workDir)
	if err != nil {
		return robot.MechanismFail, err
	}
	params := pipelineRPCGSHRunRequest{
		TaskPath: taskPath,
		TaskName: taskName,
		WorkDir:  resolvedWorkDir,
		Env:      env,
		Args:     args,
	}
	resRaw, err := runPipelineRPCRequestForRoleInDir("gsh_run", params, w, r, privsepRoleForExecution(privileged), resolvedWorkDir)
	if err != nil {
		return robot.MechanismFail, err
	}
	var res pipelineRPCGSHRunResponse
	if err := json.Unmarshal(resRaw, &res); err != nil {
		return robot.MechanismFail, fmt.Errorf("decoding gsh_run response: %v", err)
	}
	if res.Error != "" {
		ret := robot.TaskRetVal(res.RetVal)
		if ret == robot.Normal {
			ret = robot.MechanismFail
		}
		return ret, errors.New(res.Error)
	}
	return robot.TaskRetVal(res.RetVal), nil
}

func runGSHGetConfigViaRPC(taskPath, taskName, workDir string, env []string, privileged bool) (*[]byte, error) {
	resolvedWorkDir, err := resolvePipelineRPCWorkDir(workDir)
	if err != nil {
		return nil, err
	}
	params := pipelineRPCGSHGetConfigRequest{
		TaskPath: taskPath,
		TaskName: taskName,
		WorkDir:  resolvedWorkDir,
		Env:      env,
	}
	resRaw, err := runPipelineRPCRequestForRoleInDir("gsh_get_config", params, nil, nil, privsepRoleForExecution(privileged), resolvedWorkDir)
	if err != nil {
		return nil, err
	}
	var res pipelineRPCGSHGetConfigResponse
	if err := json.Unmarshal(resRaw, &res); err != nil {
		return nil, fmt.Errorf("decoding gsh_get_config response: %v", err)
	}
	if res.Error != "" {
		return nil, errors.New(res.Error)
	}
	cfg := []byte(res.Config)
	return &cfg, nil
}

func handlePipelineRPCGSHRun(dec *json.Decoder, enc *json.Encoder, msg pipelineRPCMessage) error {
	var req pipelineRPCGSHRunRequest
	if err := json.Unmarshal(msg.Params, &req); err != nil {
		return writePipelineRPCError(enc, msg.ID, "invalid_params", fmt.Sprintf("invalid gsh_run params: %v", err))
	}
	client := newPipelineRPCInterpreterRobotClient(dec, enc, map[string]string{})
	ret, err := gshmod.CallExtension(req.TaskPath, req.TaskName, req.WorkDir, req.Env, client, client, req.Args)
	res := pipelineRPCGSHRunResponse{RetVal: int(ret)}
	if err != nil {
		res.Error = err.Error()
	}
	return writePipelineRPCResponse(enc, msg.ID, res)
}

func handlePipelineRPCGSHGetConfig(enc *json.Encoder, msg pipelineRPCMessage) error {
	var req pipelineRPCGSHGetConfigRequest
	if err := json.Unmarshal(msg.Params, &req); err != nil {
		return writePipelineRPCError(enc, msg.ID, "invalid_params", fmt.Sprintf("invalid gsh_get_config params: %v", err))
	}
	cfg, err := gshmod.GetPluginConfig(req.TaskPath, req.TaskName, req.WorkDir, req.Env, nil)
	res := pipelineRPCGSHGetConfigResponse{}
	if err != nil {
		res.Error = err.Error()
	} else if cfg != nil {
		res.Config = string(*cfg)
	}
	return writePipelineRPCResponse(enc, msg.ID, res)
}
