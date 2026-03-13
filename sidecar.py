# Image inference server. Follows the protocol described in the README to
# serve on the Unix domain socket named with --socketfile.
#
# Has to be run from inside a virtualenv created following the README
# instructions.
#
# Adated from Eli Bendersky [https://eli.thegreenplace.net]
# This code is in the public domain.
import argparse
import base64
import gc
import json
import random
import time
from io import BytesIO

import apsw
import apsw.bestpractice
import apsw.fts5
import mobileclip
import torch
import vectorlite_py
import win32file
import win32pipe
from PIL import Image
from sentence_transformers import SentenceTransformer

EPOCH = int(
    time.mktime(time.strptime("2026-01-01 00:00:00", "%Y-%m-%d %H:%M:%S")) * 1000
)


def fts5_escape(query):
    """Escape tokens for FTS5: quote only tokens with special chars,
    add prefix matching (*) to plain alphanumeric tokens for broader recall."""
    import re

    tokens = query.split()
    out = []
    for t in tokens:
        if re.search(r"[^a-zA-Z0-9]", t):
            # Contains punctuation/special chars — quote it
            out.append('"' + t.replace('"', '""') + '"')
        else:
            # Plain word — use prefix match for broader recall
            out.append(t + "*")
    return " ".join(out)


def generate_rowid():
    # ts = int(time.time() * 1000) - EPOCH  # milliseconds since epoch
    # if ts < 0 or ts >= (1 << 30):
    #     raise OverflowError("Timestamp exceeds 28 bits")

    rand = random.getrandbits(63)

    # rowid = (ts << 35) | rand
    return rand  # rowid


def upsert_files(file_paths, file_hashes, cursor):
    """Insert or update file-level hashes from batch data."""
    if file_paths and file_hashes:
        cursor.executemany(
            "INSERT INTO files(path, hash) VALUES (?, ?) ON CONFLICT(path) DO UPDATE SET hash=excluded.hash",
            list(zip(file_paths, file_hashes)),
        )


def delete_paths(data, conn):
    """Delete all references (metadata, embeddings, files) for the given paths."""
    paths = data["paths"]
    cursor = conn.cursor()
    cursor.execute("begin")
    try:
        for path in paths:
            ids = [
                row[0]
                for row in cursor.execute(
                    "SELECT id FROM metadata WHERE path = ?", (path,)
                ).fetchall()
            ]
            if ids:
                placeholders = ",".join("?" * len(ids))
                cursor.execute(
                    f"DELETE FROM text_embs WHERE rowid IN ({placeholders})", ids
                )
                cursor.execute(
                    f"DELETE FROM image_embs WHERE rowid IN ({placeholders})", ids
                )
                cursor.execute(
                    f"DELETE FROM metadata WHERE id IN ({placeholders})", ids
                )
            cursor.execute("DELETE FROM files WHERE path = ?", (path,))
        cursor.execute("commit")
    except Exception as e:
        cursor.execute("rollback")
        print(e)
        raise


def rename_paths(data, conn):
    """Rename file paths in metadata and files tables."""
    old_paths = data["old_paths"]
    new_paths = data["new_paths"]
    cursor = conn.cursor()
    cursor.execute("begin")
    try:
        for old_path, new_path in zip(old_paths, new_paths):
            cursor.execute(
                "UPDATE metadata SET path = ? WHERE path = ?", (new_path, old_path)
            )
            cursor.execute(
                "UPDATE files SET path = ? WHERE path = ?", (new_path, old_path)
            )
        cursor.execute("commit")
    except Exception as e:
        cursor.execute("rollback")
        print(e)
        raise


def index_text(data, model, conn):
    t0 = time.time()
    contents = data["content"]
    paths = data["path"]
    file_paths = data.get("file_path")
    file_hashes = data.get("file_hash")
    del data  # Free input data structure early

    embs = model.encode(
        contents,
        convert_to_numpy=True,
        # normalize_embeddings=True,
    )
    t1 = time.time()
    print(f"Encode {len(embs)} text: {t1 - t0:.2f}s")

    cursor = conn.cursor()
    cursor.execute("begin")
    try:
        upsert_files(file_paths, file_hashes, cursor)
        ids = [generate_rowid() for _ in range(len(embs))]

        t2 = time.time()
        cursor.executemany(
            "insert into metadata(id, content, path) values (?, ?, ?)",
            [(ids[i], contents[i], paths[i]) for i in range(len(ids))],
        )
        del contents, paths  # Free after insert
        t3 = time.time()
        print(f"Metadata insert: {t3 - t2:.2f}s")

        # cursor.executemany(
        #     "insert into search(id, content, path) values (?, ?, ?)",
        #     [(ids[i], data["content"][i], data["path"][i]) for i in range(len(ids))],
        # )
        # t3b = time.time()
        # print(f"FTS insert: {t3b - t3:.2f}s")

        cursor.executemany(
            "insert into text_embs(rowid, embs) values (?, ?)",
            [(ids[i], embs[i].tobytes()) for i in range(embs.shape[0])],
        )
        t4 = time.time()
        print(f"Vector insert: {t4 - t3:.2f}s")
        cursor.execute("commit")
        t5 = time.time()
        print(f"Commit: {t5 - t4:.2f}s | Total: {t5 - t0:.2f}s")
    except Exception as e:
        cursor.execute("rollback")
        print(e)
        raise
    finally:
        del embs, ids
        gc.collect()


def index_image(data, model, processor, conn):
    t0 = time.time()
    batch_size = 20  # Process in smaller batches to reduce peak memory
    all_embs = []
    paths = data["path"]
    contents = data["content"]
    file_paths = data.get("file_path")
    file_hashes = data.get("file_hash")
    del data  # Free input data early

    for batch_start in range(0, len(contents), batch_size):
        batch_end = min(batch_start + batch_size, len(contents))
        imgs = []
        for i in range(batch_start, batch_end):
            raw = base64.b64decode(contents[i])
            contents[i] = None  # Free decoded content immediately
            im = Image.open(BytesIO(raw))
            del raw
            im_proc = processor(im.convert("RGB"))
            im.close()
            imgs.append(im_proc)

        im_procs = torch.stack(imgs)
        del imgs
        with torch.no_grad():
            batch_embs = model.encode_image(im_procs).numpy()
        del im_procs
        all_embs.append(batch_embs)

    del contents
    embs = all_embs[0] if len(all_embs) == 1 else __import__("numpy").vstack(all_embs)
    del all_embs
    t1 = time.time()
    print(f"Encode {len(embs)} images: {t1 - t0:.2f}s")

    cursor = conn.cursor()
    cursor.execute("begin")
    try:
        upsert_files(file_paths, file_hashes, cursor)
        ids = [generate_rowid() for _ in range(len(embs))]

        t2 = time.time()
        cursor.executemany(
            "insert into metadata(id, content, path) values (?, ?, ?)",
            [(ids[i], "", paths[i]) for i in range(len(ids))],
        )
        del paths
        t3 = time.time()
        print(f"Metadata insert: {t3 - t2:.2f}s")

        # cursor.executemany(
        #     "insert into search(id, content, path) values (?, ?, ?)",
        #     [(ids[i], data["content"][i], data["path"][i]) for i in range(len(ids))],
        # )
        # t3b = time.time()
        # print(f"FTS insert: {t3b - t3:.2f}s")

        cursor.executemany(
            "insert into image_embs(rowid, embs) values (?, ?)",
            [(ids[i], embs[i].tobytes()) for i in range(embs.shape[0])],
        )
        t4 = time.time()
        print(f"Vector insert: {t4 - t3:.2f}s")
        cursor.execute("commit")
        t5 = time.time()
        print(f"Commit: {t5 - t4:.2f}s | Total: {t5 - t0:.2f}s")
    except Exception as e:
        cursor.execute("rollback")
        print(e)
        raise
    finally:
        del embs, ids
        gc.collect()


def search(query, tokenizer, model1, model2, conn):
    t0 = time.time()
    cursor = conn.cursor()
    with torch.no_grad():
        query_emb = model1.encode_text(tokenizer(query)).numpy()
    query_emb2 = model2.encode(query)
    t1 = time.time()
    print(f"Search encode: {t1 - t0:.2f}s")
    fts_query = fts5_escape(query)
    params = {
        "query": fts_query,
        "k": 10,
        "rrf_k": 60,
        "weight_fts": 2.0,
        "weight_vec": 1.0,
        "weight_img": 1.5,  # 50% larger for images (no BM25)
        "query_emb": query_emb.tobytes(),
        "query_emb2": query_emb2.tobytes(),
    }
    records = cursor.execute(
        """ 
        with titles as (
        select
            path,
            row_number() over (order by rank) - 1000 as rank_number
        from search
        where path match :query    
        ),
        -- text embeddings with original distance
        text_vec as (
        select
            rowid as id,
            distance as dist,
            'text' as source
        from text_embs
        where knn_search(embs, knn_param(:query_emb2, :k))
        ),
        -- image embeddings with halved distance
        image_vec as (
        select
            rowid as id,
            distance * 0.5 as dist,
            'image' as source
        from image_embs
        where knn_search(embs, knn_param(:query_emb, :k))
        ),
        -- combine and rank all vector matches together
        combined_vec as (
        select id, dist, source from text_vec
        union all
        select id, dist, source from image_vec
        ),
        vec_matches as (
        select
            id,
            source,
            row_number() over (order by dist) as rank_number
        from combined_vec
        ),
        -- the FTS5 search results
        fts_matches as (
        select
            id,
            row_number() over (order by rank) as rank_number
        from search
        where content match :query
        limit :k
        ),
        -- combine FTS5 + vector search results with RRF
        rest as (
        select
            metadata.path as path,
            -- RRF algorithm (images get 70% larger weight since no BM25)
            (
            coalesce(1.0 / (:rrf_k + fts_matches.rank_number), 0.0) * :weight_fts +
            coalesce(1.0 / (:rrf_k + vec_matches.rank_number), 0.0) * 
                case when vec_matches.source = 'image' then :weight_img else :weight_vec end
            ) as combined_rank
        from fts_matches
        full outer join vec_matches on vec_matches.id = fts_matches.id
        join metadata on metadata.id = coalesce(fts_matches.id, vec_matches.id)
        ),
        final as (
            select path, min(rank_number)  as prio
            from (
                select path, rank_number from titles
                union all
                select path, row_number() over (order by combined_rank desc) as rank_number from rest
            )
            group by path
        )
        select path 
        from final
        order by prio;
        """,
        params,
    ).fetchall()
    t2 = time.time()
    print(f"Search query: {t2 - t1:.2f}s | Total: {t2 - t0:.2f}s")
    return records


def image_search(query, tokenizer, model, conn):
    t0 = time.time()
    cursor = conn.cursor()
    with torch.no_grad():
        query_emb = model.encode_text(tokenizer(query)).numpy()
    t1 = time.time()
    print(f"Search encode: {t1 - t0:.2f}s")
    fts_query = fts5_escape(query)
    params = {
        "query": fts_query,
        "k": 10,
        "query_emb": query_emb.tobytes(),
    }
    records = cursor.execute(
        """ 
        with titles as (
        select
            path,
            row_number() over (order by rank) - 1000 as rank_number
        from search
        where path match :query    
        ),
        image_vec as (
        select
            m.path,
            row_number() over (order by i.distance) as rank_number
        from image_embs i 
        join metadata m on m.id = i.rowid
        where knn_search(embs, knn_param(:query_emb, :k))
        ),
        final as (
            select path, min(rank_number)  as prio
            from (
                select path, rank_number from titles
                union all
                select path, rank_number from image_vec
            )
            group by path
        )
        select path 
        from final
        order by prio;
        """,
        params,
    ).fetchall()
    t2 = time.time()
    print(f"Search query: {t2 - t1:.2f}s | Total: {t2 - t0:.2f}s")
    return records


def text_search(query, model, conn):
    t0 = time.time()
    cursor = conn.cursor()
    query_emb = model.encode(query)
    t1 = time.time()
    print(f"Search encode: {t1 - t0:.2f}s")
    fts_query = fts5_escape(query)
    params = {
        "query": fts_query,
        "k": 15,
        "k2": 10,
        "rrf_k": 60,
        "weight_fts": 1.0,
        "weight_vec": 1.0,
        "query_emb": query_emb.tobytes(),
    }
    records = cursor.execute(
        """ 
        with titles as (
        select
            path,
            row_number() over (order by rank) - 1000 as rank_number
        from search
        where path match :query    
        ),
        -- text embeddings with original distance
        vec_matches as (
        select
            rowid as id,
            distance as dist,
            row_number() over (order by distance) as rank_number
        from text_embs
        where knn_search(embs, knn_param(:query_emb, :k))
        ),
        -- the FTS5 search results
        fts_matches as (
        select
            id,
            row_number() over (order by rank) as rank_number
        from search
        where content match :query
        limit :k2
        ),
        -- combine FTS5 + vector search results with RRF
        rest as (
        select
            metadata.path as path,
            -- RRF algorithm (images get 70% larger weight since no BM25)
            (
            coalesce(1.0 / (:rrf_k + fts_matches.rank_number), 0.0) * :weight_fts +
            coalesce(1.0 / (:rrf_k + vec_matches.rank_number), 0.0) * :weight_vec
            ) as combined_rank
        from fts_matches
        full outer join vec_matches on vec_matches.id = fts_matches.id
        join metadata on metadata.id = coalesce(fts_matches.id, vec_matches.id)
        ),
        final as (
            select path, min(rank_number)  as prio
            from (
                select path, rank_number from titles
                union all
                select path, row_number() over (order by combined_rank desc) as rank_number from rest
            )
            group by path
        )
        select path 
        from final
        order by prio;
        """,
        params,
    ).fetchall()
    t2 = time.time()
    print(f"Search query: {t2 - t1:.2f}s | Total: {t2 - t0:.2f}s")
    return records


def optimize(cursor):
    cursor.execute("insert into search(search) values('optimize');")


def server_main():
    text_index = "text.bin"  # Path for saved vector table
    img_index = "img.bin"
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--pipe",
        type=str,
        default=r"\\.\pipe\test",
        help="The named pipe used",
    )
    parser.add_argument("--test", action="store_true", default=False)
    args = parser.parse_args()

    apsw.bestpractice.apply(apsw.bestpractice.recommended)
    if args.test:
        conn = apsw.Connection(":memory:")
    else:
        conn = apsw.Connection("filosophy.db")
    conn.enable_load_extension(True)  # enable extension loading
    conn.load_extension(vectorlite_py.vectorlite_path())  # load vectorlite
    pragma_cursor = conn.cursor()
    pragma_cursor.execute("PRAGMA journal_mode=WAL;")
    pragma_cursor.execute("PRAGMA synchronous=NORMAL;")
    pragma_cursor.execute("PRAGMA temp_store=MEMORY;")
    pragma_cursor.execute("PRAGMA mmap_size=536870912;")
    pragma_cursor.execute("PRAGMA cache_size=-200000;")
    cursor = conn.cursor()
    # check if vectorlite is loaded
    print(cursor.execute("select vectorlite_info()").fetchall())
    if args.test:
        cursor.execute(
            f"create virtual table text_embs using vectorlite(embs float32[{256}] cosine, hnsw(max_elements={100000}));"
        )

        cursor.execute(
            f"create virtual table image_embs using vectorlite(embs float32[{512}] cosine, hnsw(max_elements={100000}));"
        )
    else:
        cursor.execute(
            f"create virtual table IF NOT EXISTS text_embs using vectorlite(embs float32[{256}] cosine, hnsw(max_elements={100000}), {text_index});"
        )

        cursor.execute(
            f"create virtual table IF NOT EXISTS image_embs using vectorlite(embs float32[{512}] cosine, hnsw(max_elements={100000}), {img_index});"
        )

    cursor.execute(
        """
        CREATE TABLE IF NOT EXISTS metadata (
            id INTEGER PRIMARY KEY,
            content TEXT,
            path TEXT
        )
        """
    )

    cursor.execute(
        """
        CREATE TABLE IF NOT EXISTS files (
            path TEXT PRIMARY KEY,
            hash INTEGER NOT NULL
        )
        """
    )

    if not conn.table_exists("main", "search"):
        # create does all the hard work
        apsw.fts5.Table.create(
            conn,
            # The table will be named 'search'
            "search",
            # External content table name.  It has to be in the same
            # database.
            content="metadata",
            # We want the same columns as recipe, so pass `None`.
            columns=None,
            # Triggers ensure that changes to the content table
            # are reflected in the search table
            generate_triggers=True,
            # Use APSW recommended tokenization
            # tokenize=[
            #     "ngram",
            #     "ngrams",
            #     "3",
            # ],
            # There are many more options you can set
        )

    else:
        # Already exists so just the name is needed
        apsw.fts5.Table(conn, "search")

    print("Loading models")
    text_model = SentenceTransformer("text", truncate_dim=256)
    vision_model, _, preprocess = mobileclip.create_model_and_transforms(
        "mobileclip_s0", pretrained="image/mobileclip_s0.pt"
    )
    vision_model.eval()
    tokenizer = mobileclip.get_tokenizer("mobileclip_s0")

    print(f"Listening on {args.pipe}")
    pipe = win32pipe.CreateNamedPipe(
        args.pipe,
        win32pipe.PIPE_ACCESS_DUPLEX,
        win32pipe.PIPE_TYPE_MESSAGE
        | win32pipe.PIPE_READMODE_MESSAGE
        | win32pipe.PIPE_WAIT,
        1,
        int(100e6),
        65500,
        0,
        None,  # type: ignore
    )

    try:
        while True:
            # Wait for a connection
            win32pipe.ConnectNamedPipe(pipe, None)
            _, buf = win32file.ReadFile(pipe, int(10e6))
            while win32pipe.PeekNamedPipe(pipe, 0)[1] > 0:
                _, data = win32file.ReadFile(pipe, int(10e6))
                print("reading")
                buf += data
            print(len(buf))
            data = json.loads(buf.decode())  # type: ignore
            del buf  # Free buffer after parsing
            if data["task"] == "text":
                index_text(data["data"], text_model, conn)
                some_data = "done"
            elif data["task"] == "image":
                index_image(data["data"], vision_model, preprocess, conn)
                some_data = "done"
            elif data["task"] == "delete":
                delete_paths(data["data"], conn)
                some_data = "done"
            elif data["task"] == "rename":
                rename_paths(data["data"], conn)
                some_data = "done"
            else:
                some_data = str(
                    search(data["data"], tokenizer, vision_model, text_model, conn)
                )
            win32file.WriteFile(pipe, str.encode(some_data))
            win32pipe.DisconnectNamedPipe(pipe)
    except KeyboardInterrupt:
        print("Server shutting down")

    finally:
        conn.close()
        win32file.CloseHandle(pipe)


if __name__ == "__main__":
    server_main()
