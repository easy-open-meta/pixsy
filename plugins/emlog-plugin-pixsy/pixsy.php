<?php
/*
Plugin Name: pixsy图床
Version: 1.0.0
Plugin URL: https://github.com/ck_yeun9/pixsy
Description: 接管 emlog 资源上传，将图片上传到 pixsy 图床并使用 pixsy 返回的外链。在插件设置里填写 pixsy 站点地址与账号，支持 token 自动刷新与删除文章资源时同步删除 pixsy 上的图片。
Author: ck_yeun9
Author URL: https://github.com/ck_yeun9
*/

!defined('EMLOG_ROOT') && exit('access denied!');

/**
 * pixsy 图床插件
 *
 * 工作方式：接管 admin/media.php 的 upload_media 钩子（doOnceAction 单次接管式，
 * 本插件先于内置 upload2local 注册，故可整体接管），把临时文件通过 pixsy
 * POST /api/v1/images 上传，用返回的外链填充上传结果。
 *
 * pixsy API 契约（见 pixsy 仓库 internal/api/api.go）：
 *   POST /api/v1/auth/login   {username,password} -> {"data":{"token":...}}（token 7 天有效）
 *   POST /api/v1/images       multipart 字段 file，头 Authorization: Bearer <token>
 *                             -> {"data":{"images":[{url, thumb_url, markdown, ...}]}}
 *   DELETE /api/v1/images/{id} 同上认证
 */

// ---------------------------------------------------------------- 上传接管

// 钩子注册放在函数定义后，且兼容设置页防御式加载的场景（那时 addAction 可能未定义）
if (function_exists('addAction')) {
    addAction('upload_media', 'pixsy_upload_media');
    addAction('del_media', 'pixsy_del_media');
}

function pixsy_upload_media($attach, &$result)
{
    pixsy_log('收到上传请求', ['name' => $attach['name'] ?? '', 'size' => $attach['size'] ?? 0]);

    $opt = pixsy_get_options();
    if (empty($opt['server']) || empty($opt['username']) || empty($opt['password'])) {
        $result = pixsy_fail('请先在 pixsy 图床插件设置中填写服务器地址与账号');
        return;
    }
    if (empty($attach['tmp_name']) || !is_readable($attach['tmp_name'])) {
        $result = pixsy_fail('读取上传临时文件失败');
        return;
    }

    $token = pixsy_get_token($opt);
    if ($token === '') {
        $detail = isset($GLOBALS['pixsy_last_login_error']) ? $GLOBALS['pixsy_last_login_error'] : '';
        $result = pixsy_fail('pixsy 登录失败，请检查插件设置中的站点地址与账号' . ($detail !== '' ? '（' . $detail . '）' : ''));
        return;
    }

    $resp = pixsy_upload_file($opt['server'], $token, $attach);
    // token 失效（7 天过期）时刷新一次重试
    if ($resp['status'] === 401) {
        pixsy_log('token 已失效，刷新后重试');
        $token = pixsy_login($opt['server'], $opt['username'], $opt['password']);
        if ($token === '') {
            $detail = isset($GLOBALS['pixsy_last_login_error']) ? $GLOBALS['pixsy_last_login_error'] : '';
            $result = pixsy_fail('pixsy 认证失败，请检查插件设置中的账号与密码' . ($detail !== '' ? '（' . $detail . '）' : ''));
            return;
        }
        $resp = pixsy_upload_file($opt['server'], $token, $attach);
    }
    if ($resp['status'] !== 200 || empty($resp['images'][0]['url'])) {
        $result = pixsy_fail('pixsy 上传失败：' . ($resp['message'] ?: ('HTTP ' . $resp['status'])));
        return;
    }

    $img = $resp['images'][0];
    $fileInfo = [
        // file_path 存 pixsy 外链；存完整 http URL 时 getFileUrl 会原样输出，
        // 文章里引用的即 pixsy 外链，媒体库展示也走同一地址。
        'file_path' => $img['url'],
        'file_name' => $attach['name'],
        'mime_type' => pixsy_guess_mime(pixsy_ext($attach['name'])),
        'size'      => (int)($img['size'] ?? $attach['size']),
        'width'     => (int)($img['width'] ?? 0),
        'height'    => (int)($img['height'] ?? 0),
        // 额外记录 pixsy 图片 ID，删除资源时用于同步删除远端图片
        'pixsy_id'  => (int)($img['id'] ?? 0),
    ];
    pixsy_remember_mapping($fileInfo);

    $result = [
        'success'   => 1,
        'message'   => '上传成功（pixsy）',
        'url'       => $img['url'],
        'file_info' => $fileInfo,
    ];
}

// ---------------------------------------------------------------- 删除联动

function pixsy_del_media($filepath)
{
    $map = pixsy_load_mapping();
    $key = md5((string)$filepath);
    if (!isset($map[$key])) {
        return;
    }
    $item = $map[$key];
    if (empty($item['pixsy_id'])) {
        return;
    }
    // 优先使用上传时记录的站点地址，避免此后改配置导致删错目标
    $opt = pixsy_get_options();
    $server = !empty($item['server']) ? $item['server'] : $opt['server'];
    if ($server === '') {
        return;
    }
    $token = pixsy_get_token($opt);
    if ($token === '') {
        return;
    }
    $url = rtrim($server, '/') . '/api/v1/images/' . (int)$item['pixsy_id'];
    pixsy_http_json($url, 'DELETE', $token);
    pixsy_forget_mapping($key);
    pixsy_log('已同步删除 pixsy 图片', ['id' => $item['pixsy_id']]);
}

// ---------------------------------------------------------------- pixsy API

/**
 * 上传单个文件到 pixsy。返回 ['status'=>int, 'images'=>[], 'message'=>string]
 */
function pixsy_upload_file($server, $token, $attach)
{
    $url = rtrim($server, '/') . '/api/v1/images';
    $name = (string)$attach['name'];
    $path = (string)$attach['tmp_name'];

    if (function_exists('curl_file_create')) {
        $post = ['file' => curl_file_create($path, pixsy_guess_mime(pixsy_ext($name)), $name)];
    } else {
        // PHP < 5.5 的兼容写法
        $post = ['file' => '@' . $path];
    }
    $ch = curl_init();
    curl_setopt_array($ch, [
        CURLOPT_URL            => $url,
        CURLOPT_POST           => true,
        CURLOPT_POSTFIELDS     => $post,
        CURLOPT_HTTPHEADER     => ['Authorization: Bearer ' . $token, 'Expect:'],
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT        => 60,
        CURLOPT_SSL_VERIFYPEER => true,
    ]);
    $body = curl_exec($ch);
    $status = (int)curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $err = curl_error($ch);
    curl_close($ch);

    if ($body === false) {
        pixsy_log('curl 上传失败', ['err' => $err]);
        return ['status' => 0, 'images' => [], 'message' => $err];
    }
    return pixsy_parse_images($body, $status);
}

/**
 * 解析 pixsy 的 {"data":{"images":[...]}} 或 {"error":{"message":...}} 响应
 */
function pixsy_parse_images($body, $status)
{
    $json = json_decode((string)$body, true);
    if (!is_array($json)) {
        return ['status' => $status, 'images' => [], 'message' => '响应不是合法 JSON：' . mb_substr((string)$body, 0, 200)];
    }
    if (isset($json['error']['message'])) {
        return ['status' => $status, 'images' => [], 'message' => (string)$json['error']['message']];
    }
    $images = [];
    if (isset($json['data']['images']) && is_array($json['data']['images'])) {
        foreach ($json['data']['images'] as $img) {
            if (!empty($img['url'])) {
                $images[] = [
                    'id'     => isset($img['id']) ? (int)$img['id'] : 0,
                    'url'    => (string)$img['url'],
                    'thumb'  => isset($img['thumb_url']) ? (string)$img['thumb_url'] : '',
                    'size'   => isset($img['size']) ? (int)$img['size'] : 0,
                    'width'  => isset($img['width']) ? (int)$img['width'] : 0,
                    'height' => isset($img['height']) ? (int)$img['height'] : 0,
                    'ext'    => isset($img['ext']) ? (string)$img['ext'] : '',
                ];
            }
        }
    }
    return ['status' => $status, 'images' => $images, 'message' => ''];
}

/**
 * JSON 请求（登录、删除），返回 ['status'=>int, 'json'=>array|null]
 */
function pixsy_http_json($url, $method, $token = '', $payload = null)
{
    $ch = curl_init();
    $headers = ['Content-Type: application/json', 'Expect:'];
    if ($token !== '') {
        $headers[] = 'Authorization: Bearer ' . $token;
    }
    $opts = [
        CURLOPT_URL            => $url,
        CURLOPT_CUSTOMREQUEST  => $method,
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT        => 30,
        CURLOPT_HTTPHEADER     => $headers,
        CURLOPT_SSL_VERIFYPEER => true,
    ];
    if ($payload !== null) {
        $opts[CURLOPT_POSTFIELDS] = json_encode($payload);
    }
    curl_setopt_array($ch, $opts);
    $body = curl_exec($ch);
    $status = (int)curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $err = curl_error($ch);
    curl_close($ch);
    return ['status' => $status, 'json' => is_string($body) ? json_decode($body, true) : null, 'curl_err' => $body === false ? $err : ''];
}

function pixsy_login($server, $username, $password)
{
    $url = rtrim($server, '/') . '/api/v1/auth/login';
    $resp = pixsy_http_json($url, 'POST', '', ['username' => $username, 'password' => $password]);
    $token = '';
    if ($resp['status'] === 200 && isset($resp['json']['data']['token'])) {
        $token = (string)$resp['json']['data']['token'];
    } else {
        // 把底层原因带出来（网络不通 / 证书错误 / 账号错误 / 网关拦截等），
        // 否则用户只能看到笼统的「登录失败」无法排查。
        $detail = $resp['curl_err'] !== '' ? $resp['curl_err'] : ('HTTP ' . $resp['status'] . ' ' . mb_substr((string)json_encode($resp['json'], JSON_UNESCAPED_UNICODE), 0, 120));
        pixsy_log('pixsy 登录失败', ['detail' => $detail]);
        $GLOBALS['pixsy_last_login_error'] = $detail;
    }
    return $token;
}

/**
 * 取 token，缓存于 Storage，pixsy token 有效期 7 天，提前 1 小时刷新
 */
function pixsy_get_token($opt)
{
    $storage = Storage::getInstance('pixsy');
    $cached = $storage->getValue('token');
    $expire = (int)$storage->getValue('token_expire');
    if (is_string($cached) && $cached !== '' && $expire > time() + 3600) {
        return $cached;
    }
    $token = pixsy_login($opt['server'], $opt['username'], $opt['password']);
    if ($token !== '') {
        $storage->setValue('token', $token, 'string');
        $storage->setValue('token_expire', time() + 7 * 86400, 'number');
    }
    return $token;
}

// ---------------------------------------------------------------- 配置与映射

function pixsy_get_options()
{
    $storage = Storage::getInstance('pixsy');
    return [
        'server'   => trim((string)$storage->getValue('server')),
        'username' => trim((string)$storage->getValue('username')),
        'password' => (string)$storage->getValue('password'),
        'keep_local' => $storage->getValue('keep_local') === 'y' || $storage->getValue('keep_local') === 1,
    ];
}

/**
 * 记录 pixsy 图片 ID 映射，供删除资源时同步删除远端。
 * 存储键为 filepath 的 md5，避免长 URL 超出 Storage 单值上限。
 */
function pixsy_remember_mapping($fileInfo)
{
    if (empty($fileInfo['pixsy_id'])) {
        return;
    }
    $storage = Storage::getInstance('pixsy');
    $map = $storage->getValue('idmap');
    $map = is_array($map) ? $map : [];
    $key = md5((string)$fileInfo['file_path']);
    $map[$key] = [
        'pixsy_id' => (int)$fileInfo['pixsy_id'],
        'server'   => pixsy_get_options()['server'],
        'time'     => time(),
    ];
    // 只保留最近 500 条，防止无限膨胀
    if (count($map) > 500) {
        $map = array_slice($map, -500, null, true);
    }
    $storage->setValue('idmap', $map, 'array');
}

function pixsy_load_mapping()
{
    $storage = Storage::getInstance('pixsy');
    $map = $storage->getValue('idmap');
    return is_array($map) ? $map : [];
}

function pixsy_forget_mapping($key)
{
    $storage = Storage::getInstance('pixsy');
    $map = pixsy_load_mapping();
    unset($map[$key]);
    $storage->setValue('idmap', $map, 'array');
}

// ---------------------------------------------------------------- 辅助

function pixsy_fail($message)
{
    pixsy_log('上传失败', ['msg' => $message]);
    return ['success' => 0, 'message' => $message, 'url' => '', 'file_info' => []];
}

function pixsy_ext($name)
{
    $ext = strtolower(pathinfo($name, PATHINFO_EXTENSION));
    return preg_match('/^[a-z0-9]{1,5}$/', $ext) ? $ext : '';
}

function pixsy_guess_mime($ext)
{
    $map = [
        'jpg' => 'image/jpeg', 'jpeg' => 'image/jpeg', 'png' => 'image/png',
        'gif' => 'image/gif', 'webp' => 'image/webp', 'bmp' => 'image/bmp',
        'svg' => 'image/svg+xml', 'ico' => 'image/x-icon', 'avif' => 'image/avif',
    ];
    return isset($map[$ext]) ? $map[$ext] : 'application/octet-stream';
}

function pixsy_log($message, $context = [])
{
    $line = date('[Y-m-d H:i:s] ') . '[pixsy] ' . $message;
    if ($context) {
        $line .= ' ' . json_encode($context, JSON_UNESCAPED_UNICODE);
    }
    error_log($line);
    // 落到磁盘文件：优先插件目录，不可写则退到 emlog 一定可写的 content/cache
    // （docker 部署时 error_log 落 stderr，面板用户看不到）
    static $logFile = null;
    if ($logFile === null) {
        $candidates = [
            __DIR__ . '/pixsy.log',
            EMLOG_ROOT . '/content/cache/pixsy.log',
        ];
        $logFile = '';
        foreach ($candidates as $f) {
            if (@file_put_contents($f, '', FILE_APPEND | LOCK_EX) !== false) {
                $logFile = $f;
                break;
            }
        }
    }
    if ($logFile !== '') {
        @file_put_contents($logFile, $line . "\n", FILE_APPEND | LOCK_EX);
    }
}

// 捕获致命错误：上传 400 且页面无提示时，从这里能看到是哪一行崩的
register_shutdown_function(function () {
    $e = error_get_last();
    if ($e && in_array($e['type'], [E_ERROR, E_PARSE, E_CORE_ERROR, E_COMPILE_ERROR])) {
        pixsy_log('PHP 致命错误', ['msg' => $e['message'], 'file' => $e['file'], 'line' => $e['line']]);
    }
});

// 插件启用/卸载回调在 pixsy_callback.php（emlog 约定：主文件每请求都会加载，
// callback_init/callback_rm 是全局函数名，放主文件会与其它插件的回调文件撞名）
