use std::fs;
#[cfg(unix)]
use std::fs::OpenOptions;
#[cfg(unix)]
use std::io::ErrorKind;
#[cfg(unix)]
use std::io::Write;
use std::path::{Path, PathBuf};

#[cfg(unix)]
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};

use serde::Serialize;
use tauri::AppHandle;
use tauri::Manager;

const MANAGED_MARKER: &str = "# Managed by Reasonix Desktop — do not edit.";
const TARGET_PATH: &str = "/usr/local/bin/reasonix";

#[derive(Clone, Serialize)]
pub struct InstallResult {
    pub path: String,
    pub action: String,
    #[serde(rename = "usedAdmin")]
    pub used_admin: bool,
}

#[derive(Clone, Serialize)]
pub struct CommandStatus {
    pub path: String,
    pub state: String,
}

fn is_managed(existing: &str) -> bool {
    existing.lines().any(|line| line.trim() == MANAGED_MARKER)
}

fn command_state(existing: Option<&str>, expected: &str, is_executable: bool) -> &'static str {
    match existing {
        None => "missing",
        Some(raw) if !is_managed(raw) => "foreign",
        Some(raw) if raw == expected && is_executable => "installed",
        Some(_) => "needsUpdate",
    }
}

fn shell_quote(s: &str) -> String {
    format!("'{}'", s.replace('\'', "'\\''"))
}

fn generate_shim(node_path: &str, cli_path: &str) -> String {
    format!(
        "#!/bin/sh\n{}\nexec {} {} \"$@\"\n",
        MANAGED_MARKER,
        shell_quote(node_path),
        shell_quote(cli_path)
    )
}

fn resource_node_path(app: &AppHandle) -> Result<PathBuf, String> {
    let res = app
        .path()
        .resource_dir()
        .map_err(|e| format!("resource dir not available: {e}"))?;
    let name = if cfg!(windows) { "node.exe" } else { "node" };
    Ok(res.join(name))
}

fn resource_cli_path(app: &AppHandle) -> Result<PathBuf, String> {
    let res = app
        .path()
        .resource_dir()
        .map_err(|e| format!("resource dir not available: {e}"))?;
    Ok(res.join("dist").join("cli").join("index.js"))
}

#[cfg(unix)]
fn write_atomic(path: &Path, content: &str, mode: u32) -> Result<(), String> {
    let parent = path.parent().unwrap_or_else(|| Path::new("/"));
    for attempt in 0..16 {
        let tmp = unique_install_temp_path(parent, attempt);
        let mut file = match OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(&tmp)
        {
            Ok(file) => file,
            Err(e) if e.kind() == ErrorKind::AlreadyExists => continue,
            Err(e) => return Err(format!("create tmp: {e}")),
        };

        let result = (|| -> Result<(), String> {
            file.write_all(content.as_bytes())
                .map_err(|e| format!("write tmp: {e}"))?;
            file.sync_all().map_err(|e| format!("sync tmp: {e}"))?;
            fs::set_permissions(&tmp, fs::Permissions::from_mode(mode))
                .map_err(|e| format!("chmod: {e}"))?;
            fs::rename(&tmp, path).map_err(|e| format!("rename: {e}"))?;
            Ok(())
        })();

        if let Err(e) = result {
            let _ = fs::remove_file(&tmp);
            return Err(e);
        }

        return Ok(());
    }

    Err("could not create an exclusive temp file".to_string())
}

#[cfg(unix)]
fn unique_install_temp_path(parent: &Path, attempt: u32) -> PathBuf {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_nanos();
    parent.join(format!(
        ".reasonix.{}.{now}.{attempt}.tmp",
        std::process::id()
    ))
}

#[cfg(target_os = "macos")]
fn applescript_escape(s: &str) -> String {
    s.replace('\\', "\\\\").replace('"', "\\\"")
}

#[cfg(target_os = "macos")]
fn build_admin_install_shell_script(tmp: &Path, target: &str) -> String {
    let tmp_s = shell_quote(&tmp.to_string_lossy());
    let target_s = shell_quote(target);
    let marker_s = shell_quote(MANAGED_MARKER);
    format!(
        "mkdir -p /usr/local/bin && \
         if [ -e {target} ] && ! grep -Fxq {marker} {target}; then \
         echo 'target exists and was not installed by Reasonix Desktop' >&2; exit 77; \
         fi && \
         rm -f {target} && install -m 755 {tmp} {target}",
        tmp = tmp_s,
        target = target_s,
        marker = marker_s,
    )
}

#[cfg(target_os = "macos")]
struct AdminTempShim {
    dir: PathBuf,
    path: PathBuf,
}

#[cfg(target_os = "macos")]
impl Drop for AdminTempShim {
    fn drop(&mut self) {
        let _ = fs::remove_file(&self.path);
        let _ = fs::remove_dir(&self.dir);
    }
}

#[cfg(target_os = "macos")]
fn unique_admin_temp_dir(attempt: u32) -> PathBuf {
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_nanos();
    std::env::temp_dir().join(format!(
        "reasonix-shim-install-{}-{now}-{attempt}",
        std::process::id()
    ))
}

#[cfg(target_os = "macos")]
fn create_admin_temp_shim(content: &str) -> Result<AdminTempShim, String> {
    for attempt in 0..16 {
        let dir = unique_admin_temp_dir(attempt);
        match fs::create_dir(&dir) {
            Ok(()) => {}
            Err(e) if e.kind() == ErrorKind::AlreadyExists => continue,
            Err(e) => return Err(format!("create temp dir: {e}")),
        }

        let temp = AdminTempShim {
            path: dir.join("reasonix"),
            dir,
        };

        fs::set_permissions(&temp.dir, fs::Permissions::from_mode(0o700))
            .map_err(|e| format!("chmod temp dir: {e}"))?;

        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(&temp.path)
            .map_err(|e| format!("create temp shim: {e}"))?;
        file.write_all(content.as_bytes())
            .map_err(|e| format!("write temp shim: {e}"))?;
        file.sync_all()
            .map_err(|e| format!("sync temp shim: {e}"))?;
        drop(file);

        fs::set_permissions(&temp.path, fs::Permissions::from_mode(0o500))
            .map_err(|e| format!("chmod temp shim: {e}"))?;

        return Ok(temp);
    }

    Err("could not create an exclusive temp shim".to_string())
}

#[cfg(target_os = "macos")]
fn expected_shim(app: &AppHandle) -> Result<String, String> {
    let node_path = resource_node_path(app)?;
    let cli_path = resource_cli_path(app)?;
    Ok(generate_shim(
        &node_path.to_string_lossy(),
        &cli_path.to_string_lossy(),
    ))
}

#[cfg(target_os = "macos")]
fn try_install_as_admin(
    node_path: &Path,
    cli_path: &Path,
    action: &str,
) -> Result<InstallResult, String> {
    if !node_path.exists() {
        return Err(format!("bundled node not found at {}", node_path.display()));
    }
    if !cli_path.exists() {
        return Err(format!("bundled CLI not found at {}", cli_path.display()));
    }

    let shim = generate_shim(&node_path.to_string_lossy(), &cli_path.to_string_lossy());

    let tmp = create_admin_temp_shim(&shim)?;

    let shell_script = build_admin_install_shell_script(&tmp.path, TARGET_PATH);

    let script = format!(
        "do shell script \"{}\" with administrator privileges",
        applescript_escape(&shell_script),
    );

    let output = std::process::Command::new("osascript")
        .arg("-e")
        .arg(&script)
        .output()
        .map_err(|e| format!("osascript failed: {e}"))?;

    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);
        if stderr.contains("User canceled") || stderr.contains("canceled") {
            return Err("Permission cancelled by user.".to_string());
        }
        return Err(format!("admin install failed: {}", stderr.trim()));
    }

    Ok(InstallResult {
        path: TARGET_PATH.to_string(),
        action: action.to_string(),
        used_admin: true,
    })
}

#[cfg(target_os = "macos")]
fn try_install_direct(app: &AppHandle) -> Result<InstallResult, String> {
    let target = Path::new(TARGET_PATH);
    let node_path = resource_node_path(app)?;
    let cli_path = resource_cli_path(app)?;

    if !node_path.exists() {
        return Err(format!("bundled node not found at {}", node_path.display()));
    }
    if !cli_path.exists() {
        return Err(format!("bundled CLI not found at {}", cli_path.display()));
    }

    let shim = generate_shim(&node_path.to_string_lossy(), &cli_path.to_string_lossy());

    let action: String;
    if target.exists() {
        let existing = fs::read_to_string(target).map_err(|e| format!("read existing: {e}"))?;
        if !is_managed(&existing) {
            return Err(format!(
                "{} already exists and was not installed by Reasonix Desktop. \
                 Remove or rename it first, then try again.",
                TARGET_PATH
            ));
        }
        action = "updated".to_string();
    } else {
        action = "installed".to_string();
    }

    if let Some(parent) = target.parent() {
        fs::create_dir_all(parent).map_err(|e| format!("mkdir {}: {e}", parent.display()))?;
    }

    write_atomic(target, &shim, 0o755)?;

    Ok(InstallResult {
        path: TARGET_PATH.to_string(),
        action,
        used_admin: false,
    })
}

#[tauri::command]
pub fn reasonix_command_status(app: AppHandle) -> Result<CommandStatus, String> {
    #[cfg(target_os = "macos")]
    {
        let target = Path::new(TARGET_PATH);
        let expected = expected_shim(&app)?;
        let existing = if target.exists() {
            Some(fs::read_to_string(target).unwrap_or_default())
        } else {
            None
        };
        let is_executable = target
            .metadata()
            .map(|m| m.permissions().mode() & 0o111 != 0)
            .unwrap_or(false);
        Ok(CommandStatus {
            path: TARGET_PATH.to_string(),
            state: command_state(existing.as_deref(), &expected, is_executable).to_string(),
        })
    }

    #[cfg(not(target_os = "macos"))]
    {
        let _ = app;
        Ok(CommandStatus {
            path: TARGET_PATH.to_string(),
            state: "unsupported".to_string(),
        })
    }
}

#[tauri::command]
pub fn install_reasonix_command(app: AppHandle) -> Result<InstallResult, String> {
    #[cfg(target_os = "macos")]
    {
        match try_install_direct(&app) {
            Ok(r) => Ok(r),
            Err(e) => {
                let lower = e.to_lowercase();
                if lower.contains("permission denied")
                    || lower.contains("read-only")
                    || lower.contains("not permitted")
                {
                    let target = Path::new(TARGET_PATH);
                    let action = if target.exists() {
                        match fs::read_to_string(target) {
                            Ok(existing) if is_managed(&existing) => "updated",
                            Ok(_) => return Err(e),
                            Err(_) => return Err(e),
                        }
                    } else {
                        "installed"
                    };
                    let node_path = resource_node_path(&app)?;
                    let cli_path = resource_cli_path(&app)?;
                    try_install_as_admin(&node_path, &cli_path, action)
                } else {
                    Err(e)
                }
            }
        }
    }

    #[cfg(not(target_os = "macos"))]
    {
        let _ = app;
        Err("install command is only available on macOS".to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn shim_starts_with_shebang() {
        let shim = generate_shim("/opt/node", "/opt/cli.js");
        assert!(shim.starts_with("#!/bin/sh\n"));
    }

    #[test]
    fn shim_contains_managed_marker() {
        let shim = generate_shim("/opt/reasonix/node", "/opt/reasonix/dist/cli/index.js");
        assert!(shim.contains(MANAGED_MARKER));
    }

    #[test]
    fn shim_contains_bundled_node_and_cli_paths() {
        let shim = generate_shim("/opt/reasonix/node", "/opt/reasonix/dist/cli/index.js");
        assert!(shim.contains("/opt/reasonix/node"));
        assert!(shim.contains("/opt/reasonix/dist/cli/index.js"));
    }

    #[test]
    fn shim_forwards_arguments() {
        let shim = generate_shim("/opt/node", "/opt/cli.js");
        assert!(shim.contains("\"$@\""));
    }

    #[test]
    fn shell_quote_prevents_expansion_in_shim_paths() {
        assert_eq!(
            shell_quote("/tmp/Reasonix $HOME`whoami\""),
            "'/tmp/Reasonix $HOME`whoami\"'"
        );
        assert_eq!(
            shell_quote("/tmp/Reasonix's node"),
            "'/tmp/Reasonix'\\''s node'"
        );

        let shim = generate_shim("/tmp/Reasonix $HOME/node", "/tmp/Reasonix`bad`/cli.js");
        assert!(shim.contains("exec '/tmp/Reasonix $HOME/node' '/tmp/Reasonix`bad`/cli.js' \"$@\""));
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn admin_install_script_rechecks_managed_marker_before_copy() {
        let script = build_admin_install_shell_script(
            Path::new("/tmp/reasonix shim"),
            "/usr/local/bin/reasonix",
        );
        assert!(script.contains("grep -Fxq"));
        assert!(script.contains(MANAGED_MARKER));
        assert!(script.contains("target exists and was not installed by Reasonix Desktop"));
        assert!(script.contains("rm -f '/usr/local/bin/reasonix'"));
        assert!(script.contains("install -m 755 '/tmp/reasonix shim' '/usr/local/bin/reasonix'"));
    }

    #[cfg(unix)]
    #[test]
    fn write_atomic_uses_exclusive_temp_file_and_ignores_fixed_symlink() {
        let dir = unique_test_dir();
        fs::create_dir(&dir).unwrap();
        let fixed_tmp = dir.join(".reasonix.tmp");
        let target = dir.join("reasonix");
        let victim = dir.join("victim");
        fs::write(&victim, "do not touch").unwrap();
        std::os::unix::fs::symlink(&victim, &fixed_tmp).unwrap();

        write_atomic(&target, "safe shim", 0o755).unwrap();

        assert_eq!(fs::read_to_string(&victim).unwrap(), "do not touch");
        assert_eq!(fs::read_to_string(&target).unwrap(), "safe shim");
        assert!(fs::symlink_metadata(&fixed_tmp)
            .unwrap()
            .file_type()
            .is_symlink());
        assert_eq!(
            target.metadata().unwrap().permissions().mode() & 0o777,
            0o755
        );

        let _ = fs::remove_dir_all(&dir);
    }

    #[cfg(unix)]
    fn unique_test_dir() -> PathBuf {
        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_nanos();
        std::env::temp_dir().join(format!(
            "reasonix-install-test-{}-{now}",
            std::process::id()
        ))
    }

    #[cfg(target_os = "macos")]
    #[test]
    fn admin_temp_shim_uses_private_exclusive_path() {
        let temp = create_admin_temp_shim("#!/bin/sh\necho reasonix\n").unwrap();
        let old_fixed_path = std::env::temp_dir().join("reasonix-shim-install");

        assert_ne!(temp.path, old_fixed_path);
        assert_eq!(temp.path.file_name().unwrap(), "reasonix");
        assert!(temp
            .dir
            .file_name()
            .unwrap()
            .to_string_lossy()
            .starts_with("reasonix-shim-install-"));

        let dir_mode = temp.dir.metadata().unwrap().permissions().mode() & 0o777;
        let file_mode = temp.path.metadata().unwrap().permissions().mode() & 0o777;
        assert_eq!(dir_mode, 0o700);
        assert_eq!(file_mode, 0o500);

        let path = temp.path.clone();
        let dir = temp.dir.clone();
        drop(temp);
        assert!(!path.exists());
        assert!(!dir.exists());
    }

    #[test]
    fn managed_shim_is_recognised() {
        let shim = generate_shim("/opt/node", "/opt/cli.js");
        assert!(is_managed(&shim));
    }

    #[test]
    fn rejects_unmanaged_existing() {
        let foreign = "#!/bin/bash\necho hello\n";
        assert!(!is_managed(foreign));
    }

    #[test]
    fn rejects_empty_string() {
        assert!(!is_managed(""));
    }

    #[test]
    fn command_state_detects_missing_installed_outdated_and_foreign() {
        let expected = generate_shim("/opt/node", "/opt/cli.js");
        let old_managed = generate_shim("/old/node", "/old/cli.js");
        assert_eq!(command_state(None, &expected, false), "missing");
        assert_eq!(command_state(Some(&expected), &expected, true), "installed");
        assert_eq!(
            command_state(Some(&expected), &expected, false),
            "needsUpdate"
        );
        assert_eq!(
            command_state(Some(&old_managed), &expected, true),
            "needsUpdate"
        );
        assert_eq!(
            command_state(Some("#!/bin/sh\necho foreign\n"), &expected, true),
            "foreign"
        );
    }
}
