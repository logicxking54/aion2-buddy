# sample-aion2.ps1 — เก็บตัวอย่างทรัพยากรของเกม Aion 2 ขณะเล่น เพื่อหาจุดที่เกมกินหนัก
# วิธีใช้: เปิด PowerShell แล้วรันสคริปต์ก่อนเข้าเกม/ลงดัน ปล่อยทิ้งไว้ กด Ctrl+C เมื่อเลิก
# ผลลัพธ์: CSV ข้างสคริปต์ ส่งกลับมาให้วิเคราะห์ได้เลย
param(
  [double]$IntervalSec = 2,
  [string]$Out = "$PSScriptRoot\aion2-prof-$(Get-Date -Format yyyyMMdd-HHmmss).csv"
)

$cores = (Get-CimInstance Win32_ComputerSystem).NumberOfLogicalProcessors
$procs = @{}   # name -> last CPU seconds (เก็บ delta ต่อช่วงเวลา)
$targets = 'Aion2', 'aion2-buddy'
$prevIdleCPU = ($null)

"time,process,cpu_pct,ram_mb,threads,handles,gpu_pct,sys_cpu_pct,sys_free_gb" |
  Set-Content -Path $Out -Encoding ascii
Write-Host "sampling -> $Out  (Ctrl+C to stop)"

while ($true) {
  $now = Get-Date
  $os = Get-CimInstance Win32_OperatingSystem
  # CPU ของระบบรวม: คำนวณจาก Idle process delta (เวอร์ชัน Windows บางรุ่นไม่มีคลาส Perf counter ของโปรเซสเซอร์)
  $idleCPU = (Get-Process -Id 0).CPU
  $cpuTotal = 0
  if ($null -ne $prevIdleCPU) {
    $idleFrac = ($idleCPU - $prevIdleCPU) / $IntervalSec / $cores
    $cpuTotal = [math]::Round([math]::Max(0, [math]::Min(100, (1 - $idleFrac) * 100)), 1)
  }
  $prevIdleCPU = $idleCPU
  $freeGB = [math]::Round($os.FreePhysicalMemory / 1MB, 2)
  $gpuRows = Get-CimInstance Win32_PerfFormattedData_GPUPerformanceCounters_GPUEngine -ErrorAction SilentlyContinue

  foreach ($name in $targets) {
    $p = Get-Process -Name "$name*" -ErrorAction SilentlyContinue |
         Where-Object ProcessName -notmatch 'launcher' | Select-Object -First 1
    if (-not $p) { continue }
    $pid_ = $p.Id
    $key = "$name#$pid_"
    $prevCpu = $procs[$key]
    $procs[$key] = $p.CPU
    $cpuPct = 0
    if ($null -ne $prevCpu) {
      $cpuPct = [math]::Round((($p.CPU - $prevCpu) / $IntervalSec / $cores) * 100, 1)
    }
    $gpuPct = 0
    if ($gpuRows) {
      $gpuPct = [math]::Round((($gpuRows | Where-Object Name -match "pid_$pid_" |
        Measure-Object UtilizationPercentage -Sum).Sum), 1)
    }
    $ramMB = [math]::Round($p.WorkingSet64 / 1MB)
    "{0},{1},{2},{3},{4},{5},{6},{7},{8}" -f $now.ToString('HH:mm:ss'), $p.ProcessName,
      $cpuPct, $ramMB, $p.Threads.Count, $p.HandleCount, $gpuPct, $cpuTotal, $freeGB |
      Add-Content -Path $Out -Encoding ascii
  }
  Start-Sleep -Seconds $IntervalSec
}
