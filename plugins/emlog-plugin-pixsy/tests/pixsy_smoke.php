<?php
// pixsy 插件逻辑冒烟测试：模拟 emlog 环境，验证上传接管与 $ret 组装
// 用法: php -d error_reporting=E_ALL pixsy_smoke.php <pixsy_server> <user> <pass> [image_path]
define('EMLOG_ROOT', __DIR__);

// ---- emlog 环境桩 ----
$GLOBALS['__storage'] = [];
class Storage {
    const STRING = 'string'; const NUMBER = 'number'; const BOOLEAN = 'boolean'; const ARRAY_OBJECT = 'array';
    private $n;
    private function __construct($n) { $this->n = $n; }
    public static function getInstance($n) { return new self($n); }
    public function setValue($name, $value, $type = null) { $GLOBALS['__storage'][$name] = $value; return true; }
    public function getValue($name, $default = null) { return $GLOBALS['__storage'][$name] ?? $default; }
    public function deleteName($name) { unset($GLOBALS['__storage'][$name]); return true; }
    public function deleteAllName($c) { $GLOBALS['__storage'] = []; return true; }
    public function checkNameExist($name) { return array_key_exists($name, $GLOBALS['__storage']); }
}
$GLOBALS['__hooks'] = [];
function addAction($hook, $func) { $GLOBALS['__hooks'][$hook][] = $func; }

require dirname(__DIR__) . '/pixsy.php';

// 检查钩子注册
if (($GLOBALS['__hooks']['upload_media'] ?? []) !== ['pixsy_upload_media']) { fwrite(STDERR, "FAIL: upload_media hook not registered\n"); exit(1); }
if (($GLOBALS['__hooks']['del_media'] ?? []) !== ['pixsy_del_media']) { fwrite(STDERR, "FAIL: del_media hook not registered\n"); exit(1); }
echo "PASS: hooks registered\n";

// 无配置时返回失败而不是崩溃
$ret = '';
$attach = ['name' => 'x.png', 'tmp_name' => __FILE__, 'size' => 1, 'error' => 0];
pixsy_upload_media($attach, $ret);
if ($ret['success'] !== 0 || $ret['url'] !== '') { fwrite(STDERR, "FAIL: unconfigured should fail gracefully: " . json_encode($ret) . "\n"); exit(1); }
echo "PASS: graceful fail without config\n";

// 有参数时走真实 API
if ($argc >= 4) {
    $server = rtrim($argv[1], '/');
    $user = $argv[2]; $pass = $argv[3];
    $img = isset($argv[4]) ? $argv[4] : __FILE__;
    if (!is_file($img)) { fwrite(STDERR, "image not found: $img\n"); exit(1); }
    Storage::getInstance('pixsy')->setValue('server', $server, Storage::STRING);
    Storage::getInstance('pixsy')->setValue('username', $user, Storage::STRING);
    Storage::getInstance('pixsy')->setValue('password', $pass, Storage::STRING);

    $ret = '';
    $attach = ['name' => basename($img), 'tmp_name' => $img, 'size' => filesize($img), 'error' => 0];
    pixsy_upload_media($attach, $ret);
    echo "RET: " . json_encode($ret, JSON_UNESCAPED_UNICODE | JSON_PRETTY_PRINT) . "\n";
    if (empty($ret['success']) || empty($ret['url'])) { fwrite(STDERR, "FAIL: upload did not succeed\n"); exit(1); }
    if (empty($ret['file_info']['pixsy_id'])) { fwrite(STDERR, "FAIL: missing pixsy_id\n"); exit(1); }

    // 验证外链可达
    $ch = curl_init($ret['url']);
    curl_setopt_array($ch, [CURLOPT_NOBODY => true, CURLOPT_RETURNTRANSFER => true, CURLOPT_TIMEOUT => 15]);
    curl_exec($ch);
    $code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    curl_close($ch);
    if ($code !== 200) { fwrite(STDERR, "FAIL: url not reachable, http $code\n"); exit(1); }
    echo "PASS: external url reachable (HTTP $code)\n";

    // 用 file_info 走一遍删除联动
    pixsy_remember_mapping($ret['file_info']);
    pixsy_del_media($ret['file_info']['file_path']);
    $map = pixsy_load_mapping();
    $key = md5($ret['file_info']['file_path']);
    if (isset($map[$key])) { fwrite(STDERR, "FAIL: mapping not cleared after delete\n"); exit(1); }
    echo "PASS: del_media sync removed mapping\n";
}

echo "ALL PASS\n";
