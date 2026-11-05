<?php
!defined('EMLOG_ROOT') && exit('access denied!');

/**
 * pixsy 图床插件功能页（所有登录用户可见）
 * 入口：admin/plugin_user.php?plugin=pixsy
 * 功能：把 pixsy 图库的现有资源批量导入 emlog 资源库（attachment 表），方便发文时选图。
 *
 * 与设置页同理：本页面可能在插件未启用时被访问，先防御式加载主文件。
 */
if (!function_exists('pixsy_get_options')) {
    require_once EMLOG_ROOT . '/content/plugins/pixsy/pixsy.php';
}

function plugin_user_view()
{
    $imported = isset($_GET['imported']) ? (int)$_GET['imported'] : -1;
    $skipped = isset($_GET['skipped']) ? (int)$_GET['skipped'] : 0;
    ?>
    <div class="container-fluid" style="max-width:720px">
        <h3>从 pixsy 导入资源</h3>
        <p class="text-muted small">
            把 pixsy 图库中当前账号的图片批量登记到 emlog 资源库，发文时即可直接选用。
            图片文件仍存放在 pixsy，emlog 只记录外链，不会下载到本地。
            已导入过的图片会自动跳过，可以重复执行。
        </p>
        <?php if ($imported >= 0): ?>
            <div class="alert alert-success py-2">
                导入完成：新增 <?= $imported ?> 张，跳过 <?= $skipped ?> 张（已存在）。
            </div>
        <?php endif; ?>
        <form action="../admin/plugin_user.php?plugin=pixsy" method="post" class="form" style="max-width:560px">
            <input type="hidden" name="do" value="import">
            <div class="form-group form-check">
                <input type="checkbox" name="scope_all" value="1" id="scope_all" class="form-check-input">
                <label class="form-check-label small" for="scope_all">
                    导入图库全部资源（仅 pixsy 管理员账号有效；否则只导入该账号自己的图片）
                </label>
            </div>
            <div class="form-group">
                <button type="submit" class="btn btn-sm btn-primary">开始导入</button>
                <a href="./plugin.php?plugin=pixsy" class="btn btn-sm btn-outline-secondary">返回插件设置</a>
            </div>
        </form>
    </div>
<?php
}

/**
 * 功能页无独立的保存钩子，借用设置动作分发：emlog 的 plugin_user.php 不处理 POST，
 * 因此导入逻辑挂在本文件的请求生命周期里：有 POST 即执行导入并回显结果。
 * （plugin_user.php 仅在无 action 时 include 本文件并调用 plugin_user_view，
 *  所以导入放在 view 前置处理，结果通过 GET 参数回显。）
 */
function pixsy_user_handle_post()
{
    if (empty($_POST['do']) || $_POST['do'] !== 'import') {
        return;
    }
    $scopeAll = !empty($_POST['scope_all']);
    [$imported, $skipped] = pixsy_import_gallery($scopeAll);
    // emlog 的 plugin_user.php 没有 flash 机制，用 emMsg 弹结果后跳回
    emMsg("导入完成：新增 {$imported} 张，跳过 {$skipped} 张（已存在）", '../admin/plugin_user.php?plugin=pixsy&imported=' . $imported . '&skipped=' . $skipped);
}

/**
 * 拉取 pixsy 图库并写入 emlog 资源库。
 * @return array [imported, skipped]
 */
function pixsy_import_gallery($scopeAll)
{
    $opt = pixsy_get_options();
    if (empty($opt['server']) || empty($opt['username']) || empty($opt['password'])) {
        emMsg('请先在插件设置中填写 pixsy 站点地址与账号');
    }
    $token = pixsy_get_token($opt);
    if ($token === '') {
        emMsg('pixsy 登录失败，请检查插件设置中的账号与密码');
    }

    $db = Database::getInstance();
    $table = DB_PREFIX . 'attachment';
    $uid = function_exists('UID') ? UID : 1;

    $imported = 0;
    $skipped = 0;
    $page = 1;
    $pageSize = 100;
    $base = rtrim($opt['server'], '/') . '/api/v1/images?page_size=' . $pageSize . ($scopeAll ? '&scope=all' : '');

    while (true) {
        $url = $base . '&page=' . $page;
        $resp = pixsy_http_json($url, 'GET', $token);
        if ($resp['status'] !== 200 || !is_array($resp['json']) || !isset($resp['json']['data']['images'])) {
            $detail = $resp['curl_err'] !== '' ? $resp['curl_err'] : ('HTTP ' . $resp['status']);
            pixsy_log('拉取图库失败', ['page' => $page, 'detail' => $detail]);
            emMsg('拉取 pixsy 图库失败：' . $detail . '（已导入 ' . $imported . ' 张）');
        }
        $images = $resp['json']['data']['images'];
        if (!$images) {
            break;
        }
        foreach ($images as $img) {
            $url1 = isset($img['url']) ? (string)$img['url'] : '';
            if ($url1 === '') {
                continue;
            }
            // 去重：filepath 已登记过即跳过（含此前上传时写入的外链）
            $esc = $db->escape_string($url1);
            $row = $db->once_fetch_array("SELECT aid FROM $table WHERE filepath = '$esc' LIMIT 1");
            if (!empty($row['aid'])) {
                $skipped++;
                continue;
            }
            $ext = isset($img['ext']) ? preg_replace('/[^a-z0-9]/i', '', (string)$img['ext']) : '';
            $fileInfo = [
                'file_name' => basename(parse_url($url1, PHP_URL_PATH)) ?: ('pixsy_' . (int)$img['id'] . '.' . $ext),
                'size'      => isset($img['size']) ? (int)$img['size'] : 0,
                'file_path' => $url1,
                'mime_type' => pixsy_guess_mime($ext),
                'width'     => isset($img['width']) ? (int)$img['width'] : 0,
                'height'    => isset($img['height']) ? (int)$img['height'] : 0,
            ];
            // 与 Media_Model::addMedia 相同的插入语句（这里直接写表，避免依赖媒体模型类）
            $alias = function_exists('getRandStr') ? getRandStr(16, false) : substr(md5(uniqid('', true)), 0, 16);
            $sql = "INSERT INTO $table (alias, author, sortid, filename, filesize, filepath, addtime, width, height, mimetype, thumfor)
                    VALUES('%s', '%d', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s')";
            $sql = sprintf($sql, $alias, $uid, 0,
                $db->escape_string($fileInfo['file_name']),
                (int)$fileInfo['size'],
                $esc,
                time(),
                (int)$fileInfo['width'],
                (int)$fileInfo['height'],
                $db->escape_string($fileInfo['mime_type']),
                0);
            $db->query($sql);
            $imported++;
        }
        $total = isset($resp['json']['data']['total']) ? (int)$resp['json']['data']['total'] : 0;
        if ($total > 0 && $page * $pageSize >= $total) {
            break;
        }
        $page++;
    }
    pixsy_log('图库导入完成', ['imported' => $imported, 'skipped' => $skipped]);
    return [$imported, $skipped];
}

pixsy_user_handle_post();
