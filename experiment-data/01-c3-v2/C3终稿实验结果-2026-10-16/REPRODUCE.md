# 复现入口

以下路径和命令对应本轮实际环境。所有负载串行运行，避免彼此抢占资源。

## 版本与环境

- Windows 工作目录：`C:/Users/Bocchi/Desktop/Next/.e8-worktree`。
- 实验脚本：该目录下 `docs/experiments/c3-final-2026-10-16`。
- WSL Ubuntu 实验根目录：`/home/qyinf/next-c3-final-20261016`。
- 起始 Git HEAD：`d2a711a261e7aaa1e15794b4573d132653b9ce72`；包含未提交修改，以源码与二进制清单为准。
- Go 1.27.1，Linux/amd64，`CGO_ENABLED=0`，`-tags=comet_v3 -trimpath`。
- AMD Ryzen AI 9 HX 370；进程亲和性 CPU 0–3，为四个逻辑 CPU，客体拓扑显示为两核四线程；每个 Go 进程 `GOMAXPROCS=1`。
- 服务内存存储；Next 钱包 ext4/NoSync，FastPay 钱包内存。详见 `MEASUREMENT-NOTES.md`。

## 测试与构建

在冻结源码目录 `.scratch/c3-final-frozen-2026-10-16` 执行：

```powershell
go test -tags=comet_v3 ./... -count=1
$env:GOOS='linux'
$env:GOARCH='amd64'
$env:CGO_ENABLED='0'
go build -tags=comet_v3 -trimpath -o payctl ./cmd/payctl
go build -tags=comet_v3 -trimpath -o member ./cmd/member
go build -tags=comet_v3 -trimpath -o gateway ./cmd/gateway
go build -tags=comet_v3 -trimpath -o committee ./cmd/committee
```

冻结源码保留 `go.mod` 指定的本地 CometBFT 依赖，不能只复制几份新文件后假定得到同一构建。`final-manifest.json` 与 `baseline-dependency-manifest.json` 列出主体和依赖；两个测试资产补充清单记录冻结后补入的测试文件。`freeze_build.py` 拒绝覆盖已存在的冻结目录。

## 单次真实服务

在 WSL Bash 中设置：

```bash
S=/mnt/c/Users/Bocchi/Desktop/Next/.e8-worktree/docs/experiments/c3-final-2026-10-16
R=/home/qyinf/next-c3-final-20261016
```

100 笔依赖付款链（输出目录必须不存在）：

```bash
taskset -c 0-3 env GOMAXPROCS=1 python3 "$S/run_final.py" \
  --bin "$R/final-bin" --run-dir "$R/reproduce-chain100" \
  --variant final --workload chain --length 100 --gateway-mode compact
```

有实际 CAL 报价的赔付后偿还：

```bash
taskset -c 0-3 env GOMAXPROCS=1 python3 "$S/run_final.py" \
  --bin "$R/final-bin" --run-dir "$R/reproduce-fees" \
  --variant final --workload fees --case compensated --count 1 \
  --base-fee 2 --gateway-mode compact
```

固定发送速率混合负载：

```bash
taskset -c 0-3 env GOMAXPROCS=1 python3 "$S/run_final.py" \
  --bin "$R/final-bin" --run-dir "$R/reproduce-mixed" \
  --variant final --workload mixed --rate 100 --duration 30 \
  --gateway-mode compact
```

`--fixture` 可复用启动服务前冻结的身份和初始资金，运行时仍创建独立状态目录。所有实际命令保存在各组 `status.jsonl` 和各次运行 `logs/command.json`，精确复现优先采用其中的参数并换用新的输出目录。

## 重复对照与补充实验

`run_matrix.py` 负责顺序、连续链、开关、混合负载、来源延迟和拥堵对照；`run_supplements.py` 负责独立付款、RTT、有限额度及单节点暂停。它们在 `plan.json` 中预先记录轮次和轮换顺序。

```bash
taskset -c 0-3 env GOMAXPROCS=1 python3 "$S/run_matrix.py" \
  --root "$R/reproduce-chain-matrix" --final-bin "$R/final-bin" \
  --old-bin "$R/baseline-bin" --fixtures "$R/fixtures" \
  --suite chain --repeats 7 --length 100
```

FastPay 官方构建位于 `/home/qyinf/.local/share/next-baseline-tools/fastpay-target/release`，单 worker 构建位于相邻 `fastpay-runtime-sensitivity-target/release`。脚本复用 `docs/experiments/baseline-design-2026-10-13` 中的生命周期与计时适配器。FastPay 固定提交为 `db39bb0cddc94b8c34dc235b1cfdae4c1285c6f2`；不能将两个运行时的数据混为一个基线。

## 重新统计与绘图

```bash
python3 "$S/analyze.py" --root "$R" --out "$S/analysis"
python3 "$S/audit_measurements.py" --root "$R" --data "$S/analysis"
python3 "$S/report_results.py"
```

分析器读取原始报告重新计算每轮结果，保留原非零退出码。WSL 墙钟回跳不用于付款延迟或窗口定位，采用原生单调耗时；修正依据及失败记录见测量说明。

在 Windows PowerShell 中生成矢量图和 300 dpi PNG：

```powershell
D:/python/python.exe ./plot_results.py --data ./analysis --out ./figures --audit-dir C:/Users/Bocchi/.codex/skills/nature-figure/scripts
```

绘图需要 Matplotlib、NumPy；PDF 视觉检查使用 PyMuPDF。图的统计单位是独立运行，点与范围不代表独立交易数。图题和使用建议见 `FIGURE-CAPTIONS.md`。

## 交付包中的源码与脚本

`measured-source-snapshots.zip` 包含 `baseline/` 与 `final/` 两套源码及各自的 `.scratch/comet-src` 依赖。它们才是本次实际构建输入，不能仅检出 Git HEAD 后忽略未提交改动。各包和源文件都有 SHA-256 清单。源码包中的测试 PEM 是已有测试素材。

`reproduction-scripts.zip` 保留 `docs/experiments/` 的相对目录关系，包含本轮脚本、复用的 `baseline-design-2026-10-13` 脚本和 FastPay 客户端适配器。解压后将其放入相应源码根目录即可；二进制路径通过运行参数指定。FastPay 官方构建入口为 `baseline-design-2026-10-13/build_fastpay.sh`，单 worker 变体准备和构建见 `fastpay-runtime-sensitivity-2026-10-14/README.md`；包内同时保留实际运行时补丁与 Cargo.lock。

矩阵脚本中的 WSL/FastPay 工具路径对应本机环境；迁移机器时应显式调整路径与 CPU 亲和性，并保存新环境信息。单次运行入口可直接使用 `--bin` 指定构建产物，且必须换用新的输出目录。

## 数据与异常

原始目录保存请求报告、费用阶段、实际配置、资源采样、服务日志及失败记录。`export_raw.py` 导出报告包并生成 SHA-256 清单，排除实验私钥、钱包数据库和可执行程序副本；完整原始运行仍保留在 WSL 中。不会覆盖失败运行来制造全部成功的记录。
