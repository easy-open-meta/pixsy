<?php
// 导入功能冒烟：SQLite 桩 attachment 表 + 真实 pixsy 拉取
// 用法: php pixsy_import_smoke.php <server> <user> <pass> [image_path_to_upload_first]
define('EMLOG_ROOT', __DIR__ . '/..');
error_reporting(E_ALL);

// ---- emlog 桩 ----
class Option { public static $v = []; public static function get($k){ return self::$v[$k] ?? null; } }
class Cache {
    public static function getInstance(){ return new self; }
    public function updateCache($n){}
}
class Database {
    private static $i; public $pdo;
    private function __construct(){
        $this->pdo = new PDO('sqlite::memory:');
        $this->pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
        // 建表在 pixsy_user.php 首次调用时进行（DB_PREFIX 在这里定义后拼接）
    }
    public static function getInstance(){ return self::$i ??= new self; }
    public function query($sql){ $this->pdo->exec($sql); return true; }
    public function once_fetch_array($sql){
        $st = $this->pdo->query($sql); $r = $st->fetch(PDO::FETCH_ASSOC);
        return $r === false ? null : $r;
    }
    public function escape_string($s){ return substr($this->pdo->quote((string)$s), 1, -1); }
}
const DB_PREFIX = 'emlog_';
define('UID', 1);

// 建桩表（DB_PREFIX 常量已定义）
Database::getInstance()->pdo->exec("CREATE TABLE " . DB_PREFIX . "attachment (aid INTEGER PRIMARY KEY AUTOINCREMENT, alias TEXT, author INTEGER, sortid INTEGER, filename TEXT, filesize INTEGER, filepath TEXT, addtime INTEGER, width INTEGER, height INTEGER, mimetype TEXT, thumfor INTEGER)");

require EMLOG_ROOT . '/pixsy.php'; // 先加载主文件拿到 pixsy_http_json 等函数

// 简化的 Storage 桩（主文件不用桩，这里给后面用）
class Storage {
    public static $data = [];
    private function __construct(){}
    public static function getInstance($n){ static $i=null; return $i ??= new self; }
    public function setValue($n,$v,$t=null){ self::$data[$n]=$v; return true; }
    public function getValue($n,$d=null){ return self::$data[$n] ?? $d; }
    public function deleteName($n){ unset(self::$data[$n]); return true; }
    public function checkNameExist($n){ return array_key_exists($n,self::$data); }
    public function deleteAllName($c){ self::$data=[]; return true; }
}

// 上传一张测试图到 pixsy（导入的原料）
$server = $argv[1]; $user = $argv[2]; $pass = $argv[3];
if (isset($argv[4]) && is_file($argv[4])) {
    $login = pixsy_http_json(rtrim($server, '/') . '/api/v1/auth/login', 'POST', '', ['username' => $user, 'password' => $pass]);
    $token = $login['json']['data']['token'] ?? '';
    if (!$token) { fwrite(STDERR, "login failed\n"); exit(1); }
    $ch = curl_init(rtrim($server, '/') . '/api/v1/images');
    curl_setopt_array($ch, [CURLOPT_POST=>1, CURLOPT_HTTPHEADER=>['Authorization: Bearer '.$token,'Expect:'],
        CURLOPT_POSTFIELDS=>['file'=>curl_file_create($argv[4], 'image/png', 'import_src.png')], CURLOPT_RETURNTRANSFER=>1, CURLOPT_TIMEOUT=>30]);
    $up = json_decode(curl_exec($ch), true); curl_close($ch);
    if (empty($up['data']['images'][0]['url'])) { fwrite(STDERR, "seed upload failed\n"); exit(1); }
    echo "seed uploaded: " . $up['data']['images'][0]['url'] . "\n";
}

require EMLOG_ROOT . '/pixsy_user.php';
// pixsy_user.php 末尾会执行 pixsy_user_handle_post()，无 POST 时直接返回，安全

// 配置
$sto = Storage::getInstance('pixsy');
$sto->setValue('server', $server, 'string');
$sto->setValue('username', $user, 'string');
$sto->setValue('password', $pass, 'string');

// 第一次导入（emMsg 会 exit，用 error handler 捕获其输出）
ob_start();
try { pixsy_import_gallery(false); } catch (ExitOrMsg $e) {}
$out = ob_get_clean();
$db = Database::getInstance();
$count1 = (int)$db->pdo->query("SELECT COUNT(*) FROM emlog_attachment")->fetchColumn();
echo "after 1st import: rows=$count1\n";
if ($count1 < 1) { fwrite(STDERR, "FAIL: expected >=1 row after import\n"); exit(1); }
$row = $db->pdo->query("SELECT filepath, filename, width, mimetype FROM emlog_attachment LIMIT 1")->fetch(PDO::FETCH_ASSOC);
echo "row: " . json_encode($row, JSON_UNESCAPED_UNICODE) . "\n";
if (strpos($row['filepath'], 'http') !== 0) { fwrite(STDERR, "FAIL: filepath not a URL\n"); exit(1); }

// 第二次导入：应全部跳过（去重）
[$imp2, $skip2] = pixsy_import_gallery(false);
echo "2nd import: imported=$imp2 skipped=$skip2\n";
$count2 = (int)$db->pdo->query("SELECT COUNT(*) FROM emlog_attachment")->fetchColumn();
if ($imp2 !== 0 || $count2 !== $count1) { fwrite(STDERR, "FAIL: dedup broken (rows $count1 -> $count2)\n"); exit(1); }
echo "PASS: import + dedup\n";
echo "ALL PASS\n";
