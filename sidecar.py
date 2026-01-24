#
# Adated from Eli Bendersky [https://eli.thegreenplace.net]
import argparse
import base64
import json
import os
import random
import time
from io import BytesIO
from multiprocessing import Process, Queue, cpu_count

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

os.environ["TOKENIZERS_PARALLELISM"] = "false"
torch.set_num_threads(1)

print("Loading models")
text_model = SentenceTransformer("text", truncate_dim=256)
vision_model, _, preprocess = mobileclip.create_model_and_transforms(
    "mobileclip_s0", pretrained="image/mobileclip_s0.pt"
)
vision_model.eval()
tokenizer = mobileclip.get_tokenizer("mobileclip_s0")

parser = argparse.ArgumentParser()
parser.add_argument("--pipe", default=r"\\.\pipe\test")
parser.add_argument("--db", default="filosophy.db")
args = parser.parse_args()

apsw.bestpractice.apply(apsw.bestpractice.recommended)
conn = apsw.Connection(args.db)
conn.enable_load_extension(True)
conn.load_extension(vectorlite_py.vectorlite_path())

cur = conn.cursor()
cur.execute("PRAGMA journal_mode=WAL;")
cur.execute("PRAGMA synchronous=NORMAL;")
cur.execute("PRAGMA temp_store=MEMORY;")
cur.execute("PRAGMA mmap_size=536870912;")
cur.execute("PRAGMA cache_size=-200000;")

cur.execute(
    "create virtual table if not exists text_embs using vectorlite(embs float32[256] cosine)"
)
cur.execute(
    """
    create table if not exists metadata(
        id integer primary key,
        content text,
        path text
    )
    """
)

if not conn.table_exists("main", "search"):
    apsw.fts5.Table.create(
        conn,
        "search",
        content="metadata",
        columns=None,
        generate_triggers=True,
    )

EPOCH = int(
    time.mktime(time.strptime("2026-01-01 00:00:00", "%Y-%m-%d %H:%M:%S")) * 1000
)


def generate_rowid():
    # ts = int(time.time() * 1000) - EPOCH  # milliseconds since epoch
    # if ts < 0 or ts >= (1 << 30):
    #     raise OverflowError("Timestamp exceeds 28 bits")

    rand = random.getrandbits(63)

    # rowid = (ts << 35) | rand
    return rand  # rowid


def search(query, conn):
    global tokenizer
    global vision_model
    global text_model
    t0 = time.time()
    cursor = conn.cursor()
    with torch.no_grad():
        query_emb = vision_model.encode_text(tokenizer(query)).numpy()  # type: ignore
    query_emb2 = text_model.encode(query)
    t1 = time.time()
    print(f"Search encode: {t1 - t0:.2f}s")
    params = {
        "query": query,
        "k": 10,
        "rrf_k": 60,
        "weight_fts": 1.0,
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
        where knn_search(embs, knn_param(:query_emb, :k)) and distance <= 0.80
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


def image_search(query, conn):
    global tokenizer
    global vision_model
    t0 = time.time()
    cursor = conn.cursor()
    with torch.no_grad():
        query_emb = vision_model.encode_text(tokenizer(query)).numpy()  # type: ignore
    t1 = time.time()
    print(f"Search encode: {t1 - t0:.2f}s")
    params = {
        "query": query,
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
        where knn_search(embs, knn_param(:query_emb, :k)) and distance <= 0.80
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


def text_search(query, conn):
    global text_model
    t0 = time.time()
    cursor = conn.cursor()
    query_emb = text_model.encode(query)
    t1 = time.time()
    print(f"Search encode: {t1 - t0:.2f}s")
    params = {
        "query": query,
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


def encoder_worker(in_q, db_q):
    global text_model
    global image_model
    global processor

    while True:
        job = in_q.get()
        if job is None:
            db_q.put(None)
            break

        kind, contents, paths = job

        if kind == "text":
            embs = text_model.encode(
                contents,
                convert_to_numpy=True,
            )
        else:
            imgs = []
            for i in range(len(contents)):
                try:
                    im = Image.open(BytesIO(base64.b64decode(contents[i])))
                except Exception as e:
                    print(e)
                    print(paths[i], contents[i])
                im_proc = preprocess(im.convert("RGB"))
                imgs.append(im_proc)
            im_procs = torch.stack(imgs)
            with torch.no_grad():
                embs = vision_model.encode_image(im_procs).numpy()  # type: ignore

        db_q.put((kind, embs, contents, paths))


def sqlite_writer(in_q):
    global conn
    count = 0
    cur = conn.cursor()

    while True:
        if count == cpu_count() - 1:
            break

        job = in_q.get()

        if job is None:
            count += 1
            continue

        kind, embs, contents, paths = job

        try:
            cur.execute("begin")
            ids = [generate_rowid() for _ in range(len(embs))]

            cur.executemany(
                "insert into metadata(id, content, path) values (?, ?, ?)",
                [(ids[i], contents[i], paths[i]) for i in range(len(ids))],
            )

            if kind == "text":
                cur.executemany(
                    "insert into text_embs(rowid, embs) values (?, ?)",
                    [(ids[i], embs[i].tobytes()) for i in range(len(embs))],
                )
            else:
                cur.executemany(
                    "insert into image_embs(rowid, embs) values (?, ?)",
                    [(ids[i], embs[i].tobytes()) for i in range(embs.shape[0])],
                )

            cur.execute("commit")
        except Exception as e:
            cur.execute("rollback")
            print(e)

    conn.close()


def main():
    global conn
    encode_q = Queue(maxsize=32)
    db_q = Queue(maxsize=32)

    # Start encoder workers
    workers = []
    n_workers = max(1, cpu_count() - 1)
    for _ in range(n_workers):
        p = Process(target=encoder_worker, args=(encode_q, db_q))
        p.start()
        workers.append(p)

    # Start DB writer
    db_proc = Process(target=sqlite_writer, args=(db_q,))  # type: ignore
    db_proc.start()

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

    print(f"[server] listening on {args.pipe}")

    try:
        while True:
            win32pipe.ConnectNamedPipe(pipe, None)
            _, raw = win32file.ReadFile(pipe, int(100e6))
            data = json.loads(raw.decode())  # type: ignore

            if data["task"] != "search" and data["task"][0] != "l":
                contents = data["data"]["content"]
                paths = data["data"]["path"]

                encode_q.put((data["task"], contents, paths))
                some_data = "done"
            elif data["task"][0] == "l":
                contents = data["data"]["content"]
                paths = data["data"]["path"]

                encode_q.put((data["task"][1:], contents, paths))
                for _ in workers:
                    encode_q.put(None)

                for p in workers:
                    p.join()
                db_proc.join()
                some_data = "done"
            else:
                some_data = str(search(data["data"], conn))

            win32file.WriteFile(pipe, str.encode(some_data))
            win32pipe.DisconnectNamedPipe(pipe)

    except KeyboardInterrupt:
        print("shutting down")

    finally:
        for _ in workers:
            encode_q.put(None)

        for _ in workers:
            db_q.put(None)

        for p in workers:
            p.join()
        db_proc.join()
        win32file.CloseHandle(pipe)


if __name__ == "__main__":
    main()
